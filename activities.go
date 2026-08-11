package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
	"go.temporal.io/sdk/activity"
)

// linkPattern matches any https:// URL in a message -- the same "any link,
// let yt-dlp decide if it's a video" heuristic the bot has always used.
var linkPattern = regexp.MustCompile(`https://\S+`)

type ScanChannelInput struct {
	ChannelID string
}

type ScanChannelResult struct {
	URLs []string
}

// ScanChannelActivity walks a Discord channel's full message history,
// paginating backwards 100 messages at a time via discordgo's before-cursor,
// and collects every https:// link found.
func ScanChannelActivity(ctx context.Context, input ScanChannelInput) (ScanChannelResult, error) {
	logger := activity.GetLogger(ctx)
	s, err := discordgo.New("Bot " + os.Getenv("DISCORD_TOKEN"))
	if err != nil {
		return ScanChannelResult{}, fmt.Errorf("creating discord session: %w", err)
	}

	var urls []string
	beforeID := ""
	for {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("scanned %d links so far", len(urls)))

		msgs, err := s.ChannelMessages(input.ChannelID, 100, beforeID, "", "")
		if err != nil {
			return ScanChannelResult{}, fmt.Errorf("fetching channel messages before=%q: %w", beforeID, err)
		}
		if len(msgs) == 0 {
			break
		}

		for _, m := range msgs {
			urls = append(urls, linkPattern.FindAllString(m.Content, -1)...)
		}

		beforeID = msgs[len(msgs)-1].ID
		if len(msgs) < 100 {
			break
		}
	}

	logger.Info("channel scan complete", "channelID", input.ChannelID, "linksFound", len(urls))
	return ScanChannelResult{URLs: urls}, nil
}

type DownloadVideoInput struct {
	URL       string
	ChannelID string
}

type DownloadVideoResult struct {
	URL     string
	Skipped bool // already present in the download archive
	Error   string
}

// DownloadVideoActivity runs yt-dlp for a single URL. --download-archive
// makes this idempotent: a link already downloaded on a previous run is
// skipped rather than re-fetched, so re-running the trigger command is safe.
// Instagram links keep the bot's existing behavior of uploading the
// downloaded file(s) back into the triggering channel -- unlike before, the
// files are NOT deleted afterward, since they now live in shared storage
// (visible via filebrowser) rather than being a one-shot upload-and-discard.
func DownloadVideoActivity(ctx context.Context, input DownloadVideoInput) (DownloadVideoResult, error) {
	logger := activity.GetLogger(ctx)
	savePath := os.Getenv("SAVE_PATH")
	archivePath := filepath.Join(savePath, ".archive.log")

	cmd := exec.CommandContext(ctx, "yt-dlp",
		"-P", savePath,
		"--download-archive", archivePath,
		input.URL,
	)
	out, err := cmd.CombinedOutput()
	output := string(out)
	logger.Info("yt-dlp finished", "url", input.URL, "output", output)

	if err != nil {
		return DownloadVideoResult{URL: input.URL, Error: err.Error()}, nil
	}

	if strings.Contains(output, "has already been recorded in the archive") {
		return DownloadVideoResult{URL: input.URL, Skipped: true}, nil
	}

	if strings.HasPrefix(input.URL, "https://www.instagram") {
		if uploadErr := uploadInstagramResult(input.ChannelID, savePath); uploadErr != nil {
			logger.Warn("failed to upload instagram result back to channel", "error", uploadErr)
		}
	}

	return DownloadVideoResult{URL: input.URL}, nil
}

// uploadInstagramResult preserves the bot's previous behavior of posting
// downloaded Instagram media back into the triggering channel.
func uploadInstagramResult(channelID, savePath string) error {
	s, err := discordgo.New("Bot " + os.Getenv("DISCORD_TOKEN"))
	if err != nil {
		return err
	}

	files, err := filepath.Glob(filepath.Join(savePath, "*"))
	if err != nil {
		return err
	}

	for _, file := range files {
		if filepath.Base(file) == ".archive.log" {
			continue
		}
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		_, fileName := filepath.Split(file)
		_, sendErr := s.ChannelFileSend(channelID, fileName, f)
		f.Close()
		if sendErr != nil {
			return sendErr
		}
	}
	return nil
}
