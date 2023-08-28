package main

import (
	_ "crypto/ed25519"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
		go downloadVideo(m, s)
	}
	if m.Author.ID == s.State.User.ID {
		return
	}

	// If the message content is "!hello", reply with "Hello, <username>!"
	if m.Content == "!hello" {
		s.ChannelMessageSend(m.ChannelID, "Hello, "+m.Author.Username+"!")
	}
}

func downloadVideo(m *discordgo.MessageCreate, s *discordgo.Session) error {
	app := "yt-dlp"
	arg1 := m.Content
	arg0 := "-P"
	savePath := os.Getenv("SAVE_PATH")
	if strings.HasPrefix(m.Content, "https://www.instagram") {
		savePath = os.Getenv("IG_SAVE_PATH")
	}
	fmt.Println(app, arg0, savePath, arg1)
	cmd := exec.Command(app, arg0, savePath, arg1)
	exitCode := cmd.ProcessState.ExitCode()

	stdout, err := cmd.CombinedOutput()
	fmt.Println(string(stdout))
	if strings.HasPrefix(m.Content, "https://www.instagram") {
		time.Sleep(2 * time.Second)
		dir := os.Getenv("IG_SAVE_PATH")

		// Get a list of all files in the "media" folder.
		files, err := filepath.Glob(filepath.Join(dir, "*"))
		if err != nil {
			panic(err)
		}

		// Check if any files were found.
		if len(files) == 0 {
			s.ChannelMessageSend(m.ChannelID, "No files found in the 'media' folder.")
			return nil
		}

		// Send each file in the folder.
		for _, file := range files {
			f, err := os.Open(file)
			if err != nil {
				fmt.Println("Error opening file:", err)
				continue // Skip this file and continue with the next.
			}
			defer f.Close()

			_, fileName := filepath.Split(file)
			s.ChannelFileSend(m.ChannelID, fileName, f)
			err = os.Remove(file)
		}
	}
	if err != nil {
		fmt.Println(err.Error())
		_, err := s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Video: %s downloaded successfully", m.Content))
		if err != nil {
			fmt.Println(string(stdout))
			fmt.Println("Exit code:", exitCode)
			fmt.Println("Error sending message:", err)
		}

		fmt.Println(string(stdout))
		return err
	}
	fmt.Println(string(stdout))
	return nil
}
