package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

func main() {
	// Replace "YOUR_BOT_TOKEN" with your bot's token
	godotenv.Load()
	token := os.Getenv("DISCORD_TOKEN")

	// Create a new Discord session using the provided bot token.
	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		fmt.Println("Error creating Discord session:", err)
		return
	}

	// Register a callback for when the bot receives a "messageCreate" event.
	dg.AddHandler(messageCreate)

	// Open a connection to the Discord API.
	err = dg.Open()
	if err != nil {
		fmt.Println("Error opening connection to Discord:", err)
		return
	}

	fmt.Println("Bot is now running. Press Ctrl+C to exit.")

	// Wait for a signal to gracefully exit the bot.
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly close the Discord session before exiting.
	dg.Close()
}

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Ignore messages sent by the bot itself to prevent infinite loops.
	if strings.HasPrefix(m.Content, "https://") {
		go downloadVid(m, s)
	}
	if m.Author.ID == s.State.User.ID {
		return
	}
	fmt.Println(m.Content)

	// If the message content is "!hello", reply with "Hello, <username>!"
	if m.Content == "!hello" {
		s.ChannelMessageSend(m.ChannelID, "Hello, "+m.Author.Username+"!")
	}
}

func downloadVid(m *discordgo.MessageCreate, s *discordgo.Session) {
	// Attempt to download the video.
	resp, err := http.Get("http://192.168.1.18:8000/" + m.Content)
	if err != nil {
		fmt.Println("Error downloading video:", err)
		return
	}
	defer resp.Body.Close()

	// Check if the download was successful (you can add more checks if needed).
	if resp.StatusCode == http.StatusOK {
		// Send a success message to the channel.
		_, err := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Video: %s downloaded successfully", m.Content))
		if err != nil {
			fmt.Println("Error sending message:", err)
		}
	} else {
		fmt.Println("Video download failed with status code:", resp.StatusCode)
	}
}
