package main

import (
	"bytes"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	_ "modernc.org/sqlite"
)

var (
	db *sql.DB
)

func main() {
	var err error
	db, err = sql.Open("sqlite", "./channels.db")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	err = createTables()
	if err != nil {
		log.Fatal(err)
	}

	err = godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	token := os.Getenv("DISCORD_BOT_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_BOT_TOKEN environment variable is not set.")
	}

	translateShellPath := os.Getenv("TRANSLATE_PATH")
	if translateShellPath == "" {
		log.Fatal("TRANSLATE_PATH environment variable is not set.")
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

func createTables() error {
	query := `
	CREATE TABLE IF NOT EXISTS channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		server_id TEXT NOT NULL,
		channel_id1 TEXT,
		channel_id2 TEXT,
		UNIQUE(server_id)
	);
	`
	_, err := db.Exec(query)
	return err
}

func registerCommands(s *discordgo.Session) {
	_, err := s.ApplicationCommandCreate(s.State.User.ID, "", &discordgo.ApplicationCommand{
		Name:        "translate",
		Description: "Set the channels for translation",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Name:        "channel1",
				Description: "First channel to set for translation",
				Type:        discordgo.ApplicationCommandOptionChannel,
				Required:    false,
			},
			{
				Name:        "channel2",
				Description: "Second channel to set for translation",
				Type:        discordgo.ApplicationCommandOptionChannel,
				Required:    false,
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
			var channel1, channel2 *discordgo.Channel
			for _, option := range options {
				if option.Name == "channel1" {
					channel1 = option.ChannelValue(s)
				} else if option.Name == "channel2" {
					channel2 = option.ChannelValue(s)
				}
			}

			if channel1 == nil && channel2 == nil {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: "Error: At least one channel must be provided.",
					},
				})
				return
			}

			err := addTranslateChannels(i.GuildID, channel1, channel2)
			if err != nil {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: fmt.Sprintf("Failed to enable translation for channels: %s", err.Error()),
					},
				})
				return
			}

			responseContent := "Translation enabled for"
			if channel1 != nil {
				responseContent += fmt.Sprintf(" channel 1: %s", channel1.Mention())
			}
			if channel2 != nil {
				if channel1 != nil {
					responseContent += " and"
				}
				responseContent += fmt.Sprintf(" channel 2: %s", channel2.Mention())
			}
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: responseContent,
				},
			})
		}
	}
}

func addTranslateChannels(serverID string, channel1, channel2 *discordgo.Channel) error {
	var existingChannelID1, existingChannelID2 string
	err := db.QueryRow("SELECT channel_id1, channel_id2 FROM channels WHERE server_id = ?", serverID).Scan(&existingChannelID1, &existingChannelID2)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	if channel1 != nil {
		existingChannelID1 = channel1.ID
	}
	if channel2 != nil {
		existingChannelID2 = channel2.ID
	}

	_, err = db.Exec("INSERT OR REPLACE INTO channels (server_id, channel_id1, channel_id2) VALUES (?, ?, ?)", serverID, existingChannelID1, existingChannelID2)
	return err
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID || !isTranslateChannel(m.ChannelID) {
		return
	}

	if isOnlyEmoji(m.Content) {
		return
	}

	words := strings.Fields(m.Content)
	if len(words) == 1 {
		return
	}

	if len(words) == 2 && strings.ToLower(words[0]) == "en" {
		m.Content = words[1]
	}

	translatedText, err := translateToEnglish(m.Content)
	if err != nil {
		log.Println("Error translating message,", err)
		return
	}

	if areTextsSimilar(m.Content, translatedText) {
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Translated: %s", translatedText))
}

func isTranslateChannel(channelID string) bool {
	row := db.QueryRow("SELECT COUNT(*) FROM channels WHERE channel_id1 = ? OR channel_id2 = ?", channelID, channelID)
	var count int
	row.Scan(&count)
	return count > 0
}

// func detectLanguage(text string) string {
// 	info := whatlanggo.Detect(text)
// 	return whatlanggo.LangToString(info.Lang)
// }

func translateToEnglish(text string) (string, error) {
	translateShellPath := os.Getenv("TRANSLATE_PATH")
	if translateShellPath == "" {
		return "", fmt.Errorf("TRANSLATE_PATH environment variable is not set")
	}

	cmd := exec.Command(translateShellPath, "-b", ":en")

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	cmd.Stdin = strings.NewReader(text)

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("cmd.Run() failed with %s: %s", err, stderr.String())
	}

	return strings.TrimSpace(out.String()), nil
}

func areTextsSimilar(original, translated string) bool {
	original = strings.ToLower(strings.TrimSpace(original))
	translated = strings.ToLower(strings.TrimSpace(translated))

	if original == translated {
		return true
	}

	originalWords := strings.Fields(original)
	translatedWords := strings.Fields(translated)

	diffCount := 0
	for i := range originalWords {
		if i >= len(translatedWords) || originalWords[i] != translatedWords[i] {
			diffCount++
			if diffCount > 2 {
				return false
			}
		}
	}

	return true
}

func isOnlyEmoji(s string) bool {
	for _, r := range s {
		if !isEmoji(r) {
			return false
		}
	}
	return true
}

func isEmoji(r rune) bool {
	return unicode.Is(unicode.S, r) || unicode.Is(unicode.So, r) || unicode.Is(unicode.Mn, r)
}
