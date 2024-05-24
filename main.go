package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

var translateChannelID string

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_BOT_TOKEN environment variable is not set.")
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("Error creating Discord session,", err)
	}

	dg.AddHandler(messageCreate)
	dg.AddHandler(interactionCreate)

	err = dg.Open()
	if err != nil {
		log.Fatal("Error opening Discord session,", err)
	}

	registerCommands(dg)

	log.Println("Bot is running. Press CTRL+C to exit.")
	select {}
}

func registerCommands(s *discordgo.Session) {
	_, err := s.ApplicationCommandCreate(s.State.User.ID, "", &discordgo.ApplicationCommand{
		Name:        "translate",
		Description: "Set the channel for translation",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "channel",
				Description: "Channel to set for translation",
				Type:        discordgo.ApplicationCommandOptionChannel,
				Required:    true,
			},
		},
	})

	if err != nil {
		log.Fatalf("Cannot create slash command: %v", err)
	}
}

func interactionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionApplicationCommand {
		if i.ApplicationCommandData().Name == "translate" {
			options := i.ApplicationCommandData().Options
			for _, option := range options {
				if option.Name == "channel" {
					channel := option.ChannelValue(s)
					translateChannelID = channel.ID
					s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
						Type: discordgo.InteractionResponseChannelMessageWithSource,
						Data: &discordgo.InteractionResponseData{
							Content: fmt.Sprintf("Translation enabled for channel: %s", channel.Mention()),
						},
					})
					return
				}
			}
		}
	}
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID || m.ChannelID != translateChannelID {
		return
	}

	detectedLang, err := detectLanguage(m.Content)
	if err != nil {
		log.Println("Error detecting language,", err)
		return
	}

	if detectedLang != "en" {
		translatedText, err := translateToEnglish(m.Content, detectedLang)
		if err != nil {
			log.Println("Error translating message,", err)
			return
		}
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Translated: %s", translatedText))
	}
}

func detectLanguage(text string) (string, error) {
	apiKey := os.Getenv("RAPIDAPI_KEY")
	url := "https://community-language-detection.p.rapidapi.com/detect"
	payload := strings.NewReader(fmt.Sprintf(`{"q": ["%s"]}`, text))

	req, err := http.NewRequest("POST", url, payload)
	if err != nil {
		return "", err
	}

	req.Header.Add("content-type", "application/json")
	req.Header.Add("X-RapidAPI-Key", apiKey)
	req.Header.Add("X-RapidAPI-Host", "community-language-detection.p.rapidapi.com")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Check if the content type is JSON
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return "", fmt.Errorf("expected JSON response, got %s", resp.Header.Get("Content-Type"))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	// Debugging: Print the response body
	fmt.Println("Response Body:", string(body))

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("unexpected response format or empty data")
	}

	detections, ok := data["detections"].([]interface{})
	if !ok || len(detections) == 0 {
		return "", fmt.Errorf("unexpected response format or empty detections")
	}

	firstDetection, ok := detections[0].([]interface{})
	if !ok || len(firstDetection) == 0 {
		return "", fmt.Errorf("unexpected response format or empty first detection")
	}

	detection, ok := firstDetection[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("unexpected response format for detection")
	}

	language, ok := detection["language"].(string)
	if !ok {
		return "", fmt.Errorf("language key not found or not a string")
	}

	return language, nil
}

func translateToEnglish(text string, sourceLang string) (string, error) {
	encodedText := url.QueryEscape(text)
	resp, err := http.Get(fmt.Sprintf("https://api.mymemory.translated.net/get?q=%s&langpair=%s|en", encodedText, sourceLang))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	// Check if the response status is not 200 OK
	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected response status: %d, body: %s", resp.StatusCode, string(body))
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	// Check if the content type is JSON
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		return "", fmt.Errorf("expected JSON response, got %s", resp.Header.Get("Content-Type"))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}

	// Check for the existence of responseData
	responseData, ok := result["responseData"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("responseData key not found or not a map")
	}

	// Check for the existence of translatedText
	translatedText, ok := responseData["translatedText"].(string)
	if !ok {
		return "", fmt.Errorf("translatedText key not found or not a string")
	}

	return translatedText, nil
}
