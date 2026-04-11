package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

var (
	WebhookURL string
	// Reuse a single HTTP client globally
	httpClient = &http.Client{Timeout: 15 * time.Second}
)

type Payload struct {
	Content   string `json:"content"`
	Author    string `json:"author"`
	ChannelID string `json:"channel_id"`
}

type ResponsePayload struct {
	ChannelID string `json:"channel_id"`
	Message   string `json:"message"`
}

func main() {
	err := godotenv.Load()
	if err != nil {
		log.Println("No .env file found, relying on system environment variables.")
	}

	WebhookURL = os.Getenv("WEBHOOK_URL")
	if WebhookURL == "" {
		log.Fatal("WEBHOOK_URL not set")
	}

	token := os.Getenv("DISCORD_TOKEN")
	if token == "" {
		log.Fatal("DISCORD_TOKEN not set")
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		log.Fatal("Error creating session:", err)
	}

	// CRITICAL: Request necessary intents to read message content
	dg.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsDirectMessages | discordgo.IntentMessageContent

	dg.AddHandler(messageCreate)

	err = dg.Open()
	if err != nil {
		log.Fatal("Error opening connection:", err)
	}

	go func() {
		http.HandleFunc("/respond", responseHandler(dg))
		log.Println("Response server running on :8080")
		log.Fatal(http.ListenAndServe(":8080", nil))
	}()

	log.Println("Bot is running. Press CTRL-C to exit.")

	// Graceful shutdown
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt)
	<-sc

	log.Println("Shutting down cleanly...")
	dg.Close()
}

func sendLongMessage(s *discordgo.Session, channelID, message string) {
	const maxLen = 2000

	for len(message) > maxLen {
		part := message[:maxLen]
		s.ChannelMessageSend(channelID, part)
		message = message[maxLen:]
	}

	if len(message) > 0 {
		s.ChannelMessageSend(channelID, message)
	}
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore all bot messages
	if m.Author.Bot {
		return
	}

	botID := s.State.User.ID
	lowerContent := strings.ToLower(m.Content)

	// Check if bot is mentioned
	mentioned := false
	for _, user := range m.Mentions {
		if user.ID == botID {
			mentioned = true
			break
		}
	}

	// Check for commands using HasPrefix instead of Contains
	isCommand := strings.HasPrefix(lowerContent, "!todo") ||
		strings.HasPrefix(lowerContent, "!list") ||
		strings.HasPrefix(lowerContent, "!done")

	// Only process if relevant command or mention
	if !mentioned && !isCommand {
		return
	}

	// Clean message (remove bot mention)
	cleanContent := m.Content
	if mentioned {
		cleanContent = strings.ReplaceAll(cleanContent, "<@"+botID+">", "")
		cleanContent = strings.ReplaceAll(cleanContent, "<@!"+botID+">", "")
	}

	cleanContent = strings.TrimSpace(cleanContent)
	log.Println("Received:", cleanContent)

	payload := Payload{
		Content:   cleanContent,
		Author:    m.Author.Username,
		ChannelID: m.ChannelID,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Println("JSON error:", err)
		return
	}

	req, err := http.NewRequest("POST", WebhookURL, bytes.NewBuffer(body))
	if err != nil {
		log.Println("Request error:", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	// Use the global client
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Println("Webhook error:", err)
		return
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		log.Println("Decode error:", err)
		return
	}

	// Send response back to Discord
	if msg, ok := result["message"].(string); ok && msg != "" {
		sendLongMessage(s, m.ChannelID, msg)
	}
}

func responseHandler(s *discordgo.Session) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var payload ResponsePayload

		err := json.NewDecoder(r.Body).Decode(&payload)
		if err != nil {
			http.Error(w, "Invalid request", http.StatusBadRequest)
			return
		}

		sendLongMessage(s, payload.ChannelID, payload.Message)

		w.WriteHeader(http.StatusOK)
	}
}
