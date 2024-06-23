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
	db                *sql.DB
	bannedWords       map[string]struct{}
	translateChannels map[string][3]string
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

	err = loadBannedWords()
	if err != nil {
		log.Fatal(err)
	}

	err = loadTranslateChannels()
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
	channelTableQuery := `CREATE TABLE IF NOT EXISTS channels (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		server_id TEXT NOT NULL,
		channel_id1 TEXT,
		channel_id2 TEXT,
		channel_id3 TEXT,
		channel_id4 TEXT,
		channel_id5 TEXT,
		UNIQUE(server_id)
	);`

	wordbanTableQuery := `CREATE TABLE IF NOT EXISTS wordban (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		word TEXT NOT NULL UNIQUE
	);`

	_, err := db.Exec(channelTableQuery)
	if err != nil {
		return err
	}
	_, err = db.Exec(wordbanTableQuery)
	return err
}

func loadBannedWords() error {
	rows, err := db.Query("SELECT word FROM wordban")
	if err != nil {
		return err
	}
	defer rows.Close()

	bannedWords = make(map[string]struct{})
	for rows.Next() {
		var word string
		if err := rows.Scan(&word); err != nil {
			return err
		}
		bannedWords[word] = struct{}{}
	}

	return nil
}

func loadTranslateChannels() error {
	rows, err := db.Query("SELECT server_id, channel_id1, channel_id2, channel_id3 FROM channels")
	if err != nil {
		return err
	}
	defer rows.Close()

	translateChannels = make(map[string][3]string)
	for rows.Next() {
		var serverID sql.NullString
		var channelID1, channelID2, channelID3 sql.NullString
		if err := rows.Scan(&serverID, &channelID1, &channelID2, &channelID3); err != nil {
			return err
		}
		translateChannels[serverID.String] = [3]string{
			channelID1.String,
			channelID2.String,
			channelID3.String,
		}
	}

	return nil
}

func registerCommands(s *discordgo.Session) {
	commands := []*discordgo.ApplicationCommand{
		{
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
				{
					Name:        "channel3",
					Description: "Third channel to set for translation",
					Type:        discordgo.ApplicationCommandOptionChannel,
					Required:    false,
				},
			},
		},
		{
			Name:        "banword",
			Description: "Manage banned words",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Name:        "add",
					Description: "Add words to the ban list (comma separated)",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "words",
							Description: "Words to add",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "remove",
					Description: "Remove a word from the ban list",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandOption{
						{
							Name:        "word",
							Description: "Word to remove",
							Type:        discordgo.ApplicationCommandOptionString,
							Required:    true,
						},
					},
				},
				{
					Name:        "list",
					Description: "List all banned words",
					Type:        discordgo.ApplicationCommandOptionSubCommand,
				},
			},
		},
	}

	for _, command := range commands {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, "", command)
		if err != nil {
			log.Fatalf("Cannot create slash command: %v", err)
		}
	}
}

func interactionCreate(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.ApplicationCommandData().Name {
	case "translate":
		handleTranslateCommand(s, i)
	case "banword":
		handleBanwordCommand(s, i)
	}
}

func handleTranslateCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	var channel1, channel2, channel3 *discordgo.Channel
	for _, option := range options {
		if option.Name == "channel1" {
			channel1 = option.ChannelValue(s)
		} else if option.Name == "channel2" {
			channel2 = option.ChannelValue(s)
		} else if option.Name == "channel3" {
			channel3 = option.ChannelValue(s)
		}
	}

	if channel1 == nil && channel2 == nil && channel3 == nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Error: At least one channel must be provided.",
			},
		})
		return
	}

	err := addTranslateChannels(i.GuildID, channel1, channel2, channel3)
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
	if channel3 != nil {
		if channel1 != nil || channel2 != nil {
			responseContent += " and"
		}
		responseContent += fmt.Sprintf(" channel 3: %s", channel3.Mention())
	}
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: responseContent,
		},
	})
}

func handleBanwordCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	subCommand := i.ApplicationCommandData().Options[0].Name

	switch subCommand {
	case "add":
		handleBanwordAddCommand(s, i)
	case "remove":
		handleBanwordRemoveCommand(s, i)
	case "list":
		handleBanwordListCommand(s, i)
	}
}

func handleBanwordAddCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	words := i.ApplicationCommandData().Options[0].Options[0].StringValue()
	wordList := strings.Split(words, ",")
	var addedWords []string
	for _, word := range wordList {
		word = strings.TrimSpace(strings.ToLower(word))
		if word == "" {
			continue
		}
		var count int
		err := db.QueryRow("SELECT COUNT(*) FROM wordban WHERE word = ?", word).Scan(&count)
		if err != nil {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: fmt.Sprintf("Failed to check word '%s': %s", word, err.Error()),
				},
			})
			return
		}
		if count == 0 {
			_, err = db.Exec("INSERT OR IGNORE INTO wordban (word) VALUES (?)", word)
			if err != nil {
				s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
					Type: discordgo.InteractionResponseChannelMessageWithSource,
					Data: &discordgo.InteractionResponseData{
						Content: fmt.Sprintf("Failed to add word '%s' to ban list: %s", word, err.Error()),
					},
				})
				return
			}
			addedWords = append(addedWords, word)
		}
	}

	if len(addedWords) > 0 {
		// Refresh the banned words in memory
		err := loadBannedWords()
		if err != nil {
			log.Fatalf("Failed to load banned words: %s", err.Error())
		}

		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("Added words to ban list: %s", strings.Join(addedWords, ", ")),
			},
		})
	} else {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "No new words were added to the ban list.",
			},
		})
	}
}

func handleBanwordRemoveCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	word := i.ApplicationCommandData().Options[0].Options[0].StringValue()
	word = strings.TrimSpace(strings.ToLower(word))
	if word == "" {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "No word provided to remove.",
			},
		})
		return
	}
	_, err := db.Exec("DELETE FROM wordban WHERE word = ?", word)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("Failed to remove word '%s' from ban list: %s", word, err.Error()),
			},
		})
		return
	}

	// Refresh the banned words in memory
	err = loadBannedWords()
	if err != nil {
		log.Fatalf("Failed to load banned words: %s", err.Error())
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Removed word from ban list: %s", word),
		},
	})
}

func handleBanwordListCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	rows, err := db.Query("SELECT word FROM wordban")
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("Failed to retrieve banned words: %s", err.Error()),
			},
		})
		return
	}
	defer rows.Close()

	var bannedWords []string
	for rows.Next() {
		var word string
		if err := rows.Scan(&word); err != nil {
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Content: fmt.Sprintf("Failed to scan banned word: %s", err.Error()),
				},
			})
			return
		}
		bannedWords = append(bannedWords, word)
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Banned words: %s", strings.Join(bannedWords, ", ")),
		},
	})
}

func addTranslateChannels(serverID string, channel1, channel2, channel3 *discordgo.Channel) error {
	var existingChannelID1, existingChannelID2, existingChannelID3 sql.NullString
	err := db.QueryRow("SELECT channel_id1, channel_id2, channel_id3 FROM channels WHERE server_id = ?", serverID).Scan(&existingChannelID1, &existingChannelID2, &existingChannelID3)
	if err != nil && err != sql.ErrNoRows {
		return err
	}

	if channel1 != nil {
		existingChannelID1.String = channel1.ID
		existingChannelID1.Valid = true
	}
	if channel2 != nil {
		existingChannelID2.String = channel2.ID
		existingChannelID2.Valid = true
	}
	if channel3 != nil {
		existingChannelID3.String = channel3.ID
		existingChannelID3.Valid = true
	}

	_, err = db.Exec("INSERT OR REPLACE INTO channels (server_id, channel_id1, channel_id2, channel_id3) VALUES (?, ?, ?, ?)", serverID, existingChannelID1, existingChannelID2, existingChannelID3)
	if err == nil {
		err = loadTranslateChannels()
	}
	return err
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID || !isTranslateChannel(m.ChannelID) {
		return
	}

	if isOnlyEmoji(m.Content) {
		return
	}

	if containsBannedWord(m.Content) {
		return
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
	for _, channels := range translateChannels {
		for _, chID := range channels {
			if chID == channelID {
				return true
			}
		}
	}
	return false
}

func containsBannedWord(text string) bool {
	words := strings.Fields(strings.ToLower(text))
	for _, word := range words {
		if _, exists := bannedWords[word]; exists {
			return true
		}
	}
	return false
}

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
