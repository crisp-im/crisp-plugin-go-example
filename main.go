package main

import (
	"encoding/json"
	crispAPI "github.com/crisp-im/go-crisp-api/crisp"
	log "github.com/sirupsen/logrus"
	"html/template"
	"net/http"
	"reflect"
)

const (
	pluginURN          = "urn:my.account:pluginname:0"
	crispAPIIdentifier = "change-me-please"
	crispAPIKey        = "abcdefghijklmnopqrstuvwxyz"
)

type crisp struct {
	config      *crispConfig
	crispClient *crispAPI.Client
	websites    map[string]*crispWebsite
}

type crispConfig struct {
	CrispRESTAuthIdentifier string
	CrispRESTAuthKey        string
	CrispPluginID           string
}

type crispWebsite struct {
	subscriptionToken string
	pluginSettings    *PluginSettings
}

type PluginSettings struct {
	Message string `json:"message"`
}

func main() {
	crisp := initPlugin()
	handleCrispEvents(crisp)
	httpEndpoint(crisp)
}

func initPlugin() *crisp {
	log.SetFormatter(&log.TextFormatter{
		TimestampFormat: "02-01-2006 15:04:05",
		FullTimestamp:   true,
	})
	log.SetLevel(log.DebugLevel)

	config := &crispConfig{
		CrispRESTAuthIdentifier: crispAPIIdentifier,
		CrispRESTAuthKey:        crispAPIKey,
	}

	subscribedWebsites := make(map[string]*crispWebsite)

	crisp := &crisp{
		config:      config,
		crispClient: crispAPI.New(),
		websites:    subscribedWebsites,
	}

	crisp.crispClient.AuthenticateTier("plugin", config.CrispRESTAuthIdentifier, config.CrispRESTAuthKey)
	connectAccount, response, err := crisp.crispClient.Plugin.GetConnectAccount()
	if err != nil {
		log.Errorf("Error verifying plugin connect information, err: %s, resp: %+v", err, response)
	}
	config.CrispPluginID = *connectAccount.PluginID

	response, err = crisp.loadAllSubscribedWebsites(subscribedWebsites)
	if err != nil {
		log.Error("Error loading all subscribed websites, err: %s, resp: %+v", err, response)
	}
	return crisp
}

func (crisp *crisp) loadAllSubscribedWebsites(websitesMap map[string]*crispWebsite) (resp *crispAPI.Response, err error) {
	var pageNumber uint

	pageNumber = 0
	resultCounter := -1

	for resultCounter != 0 {
		pageNumber++
		resultCounter = 0

		websites, resp, err := crisp.crispClient.Plugin.ListAllConnectWebsites(pageNumber, true)

		if err == nil {
			log.Infof("Loaded page #%d of website results (%d websites)", pageNumber, len(*websites))

			for _, websiteItem := range *websites {
				resultCounter++
				websiteCustomConfig := reflect.ValueOf(*websiteItem.Settings).Interface().(map[string]interface{})
				website := &crispWebsite{
					subscriptionToken: *websiteItem.Token,
					pluginSettings:    &PluginSettings{Message: websiteCustomConfig["message"].(string)},
				}
				websitesMap[*websiteItem.WebsiteID] = website
				log.Infof("website %s now bound with the plugin %+v", *websiteItem.WebsiteID, website)
			}
		} else {
			return resp, err
		}
	}
	return resp, err
}

func handleCrispEvents(crisp *crisp) {
	crisp.crispClient.Events.Listen(
		[]string{
			"message:received",
			"message:send",
		},

		func(reg *crispAPI.EventsRegister) {
			log.Info("Socket is connected: now listening for events")

			reg.On("message:received/text", func(evt crispAPI.EventsReceiveTextMessage) {
				log.Info("New text message in conversation")

				if *evt.Origin == pluginURN {
					log.Info("This message comes from me... Skipping this one")
					return
				}

				_, response, err := crisp.crispClient.Website.SendTextMessageInConversation(*evt.WebsiteID, *evt.SessionID, crispAPI.ConversationTextMessageNew{
					Type:    "text",
					Content: crisp.websites[*evt.WebsiteID].pluginSettings.Message,
					From:    "operator",
					Origin:  pluginURN,
					User: crispAPI.ConversationAllMessageNewUser{
						Nickname: "Ping-Pong",
						Avatar:   "https://crisp.chat/favicon-512x512.png",
					},
				})
				if err != nil {
					log.Error("Error occurred when sending message", err, response)
				}

			})

			reg.On("message:send/text", func(evt crispAPI.EventsReceiveTextMessage) {
				log.Info("message sent from user")
			})
		},

		func() {
			log.Error("Socket is disconnected: will try to reconnect")
		},

		func() {
			log.Error("Socket error: may be broken")
		},
	)
}

func httpEndpoint(crisp *crisp) {
	http.HandleFunc("/config", func(writer http.ResponseWriter, request *http.Request) {
		log.Info("Someone is configuring his plugin")
		t := template.Must(template.New("config.gohtml").ParseFiles("config.gohtml"))
		err := t.Execute(writer, nil)
		if err != nil {
			log.Fatal(err)
		}
	})

	http.HandleFunc("/config/update", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == "POST" {
			log.Info("Someone updated his plugin configuration")
			var req map[string]interface{}
			err := json.NewDecoder(request.Body).Decode(&req)
			if err != nil {
				log.Warnln(err)
			}
			websiteID := req["website_id"].(string)
			token := req["token"].(string)
			message := req["message"].(string)
			if websiteID == "" || token == "" {
				log.Error("WebsiteID or Token is missing in request.")
				return
			}
			if token != crisp.websites[websiteID].subscriptionToken {
				log.Warn("The provided token is not valid")
				return
			}
			response, err := crisp.crispClient.Plugin.UpdateSubscriptionSettings(websiteID, crisp.config.CrispPluginID, PluginSettings{Message: message})
			if err != nil {
				log.Error(err)
			}
			crisp.websites[websiteID].pluginSettings = &PluginSettings{Message: message}
			log.Infof("Successfully updated plugin settings, resp: %+v", response)
		} else {
			writer.WriteHeader(http.StatusMethodNotAllowed)
		}
	})

	log.Fatal(http.ListenAndServe(":1234", nil))
}
