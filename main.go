package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

const (
	// triggerCommand replaces the old "download on every https:// message"
	// behavior: nothing downloads until this is sent, at which point the
	// whole channel's history is scanned for links.
	triggerCommand = "!downloadall"
	taskQueue      = "video-downloads"
)

func main() {
	godotenv.Load()

	token := os.Getenv("DISCORD_TOKEN")
	temporalHost := os.Getenv("TEMPORAL_HOST")
	if temporalHost == "" {
		temporalHost = "localhost:7233"
	}

	dg, err := discordgo.New("Bot " + token)
	if err != nil {
		fmt.Println("Error creating Discord session:", err)
		return
	}

	temporalClient, err := dialTemporalWithRetry(temporalHost)
	if err != nil {
		fmt.Println("Error connecting to Temporal:", err)
		return
	}
	defer temporalClient.Close()

	w := worker.New(temporalClient, taskQueue, worker.Options{})
	w.RegisterWorkflow(DownloadChannelWorkflow)
	w.RegisterWorkflow(DownloadLinksWorkflow)
	w.RegisterActivity(ScanChannelActivity)
	w.RegisterActivity(DownloadVideoActivity)

	go func() {
		if err := w.Run(worker.InterruptCh()); err != nil {
			fmt.Println("Temporal worker stopped:", err)
		}
	}()

	dg.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
		if m.Author.ID == s.State.User.ID {
			return
		}

		if strings.TrimSpace(m.Content) == triggerCommand {
			go handleTrigger(temporalClient, s, m)
			return
		}

		// New message with a link: download just that link automatically,
		// no command needed. This does NOT re-scan history -- only the
		// !downloadall command does that, for backfill/catchup.
		if urls := linkPattern.FindAllString(m.Content, -1); len(urls) > 0 {
			go handleNewLinks(temporalClient, s, m, urls)
		}
	})

	if err := dg.Open(); err != nil {
		fmt.Println("Error opening connection to Discord:", err)
		return
	}
	defer dg.Close()

	fmt.Println("Bot is now running. Press Ctrl+C to exit.")

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc
}

// dialTemporalWithRetry retries connecting to Temporal with backoff instead
// of giving up immediately -- the worker deployment and the Temporal server
// deployment don't have a guaranteed startup order, so a bare client.Dial on
// first boot can easily race a Temporal pod that's still coming up.
func dialTemporalWithRetry(hostPort string) (client.Client, error) {
	var lastErr error
	backoff := 2 * time.Second
	const maxBackoff = 30 * time.Second

	for attempt := 1; attempt <= 10; attempt++ {
		c, err := client.Dial(client.Options{HostPort: hostPort})
		if err == nil {
			return c, nil
		}
		lastErr = err
		fmt.Printf("Temporal not reachable yet (attempt %d): %v; retrying in %s\n", attempt, err, backoff)
		time.Sleep(backoff)
		if backoff < maxBackoff {
			backoff *= 2
		}
	}
	return nil, lastErr
}

// handleTrigger starts a DownloadChannelWorkflow for the channel the command
// was sent in, and reports the outcome back once it completes.
func handleTrigger(temporalClient client.Client, s *discordgo.Session, m *discordgo.MessageCreate) {
	s.ChannelMessageSend(m.ChannelID, "Scanning channel history for links, this may take a moment...")

	workflowID := fmt.Sprintf("download-channel-%s-%d", m.ChannelID, time.Now().Unix())
	run, err := temporalClient.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: taskQueue,
	}, DownloadChannelWorkflow, DownloadChannelInput{ChannelID: m.ChannelID})
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to start download workflow: %v", err))
		return
	}

	var result DownloadChannelResult
	if err := run.Get(context.Background(), &result); err != nil {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Download workflow failed: %v", err))
		return
	}

	s.ChannelMessageSend(m.ChannelID, fmt.Sprintf(
		"Found %d links. Downloaded %d, skipped %d (already archived), failed %d.",
		result.Found, result.Downloaded, result.Skipped, result.Failed,
	))
}

// handleNewLinks downloads the link(s) found in a single new message,
// automatically, without needing the !downloadall command.
func handleNewLinks(temporalClient client.Client, s *discordgo.Session, m *discordgo.MessageCreate, urls []string) {
	workflowID := fmt.Sprintf("download-links-%s-%d", m.ChannelID, time.Now().Unix())
	run, err := temporalClient.ExecuteWorkflow(context.Background(), client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: taskQueue,
	}, DownloadLinksWorkflow, DownloadLinksInput{URLs: urls, ChannelID: m.ChannelID})
	if err != nil {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Failed to start download: %v", err))
		return
	}

	var result DownloadChannelResult
	if err := run.Get(context.Background(), &result); err != nil {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("Download failed: %v", err))
		return
	}

	if result.Failed > 0 {
		s.ChannelMessageSend(m.ChannelID, fmt.Sprintf(
			"Downloaded %d, skipped %d (already archived), failed %d.",
			result.Downloaded, result.Skipped, result.Failed,
		))
	}
	// Stay quiet on full success -- the file showing up in filebrowser is
	// confirmation enough, no need to narrate every single link.
}
