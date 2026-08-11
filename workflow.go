package main

import (
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type DownloadChannelInput struct {
	ChannelID string
}

type DownloadLinksInput struct {
	URLs      []string
	ChannelID string
}

type DownloadChannelResult struct {
	Found      int
	Downloaded int
	Skipped    int
	Failed     int
}

// DownloadChannelWorkflow scans a Discord channel's full message history for
// video links and downloads every one of them. Safe to re-run: links already
// fetched by a previous run are skipped via yt-dlp's download archive, but
// note this re-scans the *entire* history every time, which is slow on a
// busy channel -- prefer the automatic per-message trigger for new links,
// and use this one for backfill/catchup.
func DownloadChannelWorkflow(ctx workflow.Context, input DownloadChannelInput) (DownloadChannelResult, error) {
	scanCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 10 * time.Minute,
		HeartbeatTimeout:    30 * time.Second,
	})

	var scanResult ScanChannelResult
	if err := workflow.ExecuteActivity(scanCtx, ScanChannelActivity, ScanChannelInput{
		ChannelID: input.ChannelID,
	}).Get(scanCtx, &scanResult); err != nil {
		return DownloadChannelResult{}, fmt.Errorf("scanning channel: %w", err)
	}

	return downloadURLsInBatches(ctx, scanResult.URLs, input.ChannelID)
}

// DownloadLinksWorkflow downloads a specific, already-known set of links --
// used for the automatic per-message trigger, where we already have the
// URL(s) from the message itself and don't need to scan anything.
func DownloadLinksWorkflow(ctx workflow.Context, input DownloadLinksInput) (DownloadChannelResult, error) {
	return downloadURLsInBatches(ctx, input.URLs, input.ChannelID)
}

// downloadURLsInBatches is shared by both workflows above. Downloads run in
// batches rather than all at once: yt-dlp (plus ffmpeg for merging/
// transcoding) is heavy per-process, and this worker shares its node with
// etcd and Longhorn storage duty -- fanning out everything in one go is
// enough to starve the node.
func downloadURLsInBatches(ctx workflow.Context, urls []string, channelID string) (DownloadChannelResult, error) {
	downloadCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	const batchSize = 5
	result := DownloadChannelResult{Found: len(urls)}

	for start := 0; start < len(urls); start += batchSize {
		end := start + batchSize
		if end > len(urls) {
			end = len(urls)
		}
		batch := urls[start:end]

		futures := make([]workflow.Future, len(batch))
		for i, url := range batch {
			futures[i] = workflow.ExecuteActivity(downloadCtx, DownloadVideoActivity, DownloadVideoInput{
				URL:       url,
				ChannelID: channelID,
			})
		}

		for _, f := range futures {
			var r DownloadVideoResult
			if err := f.Get(ctx, &r); err != nil {
				result.Failed++
				continue
			}
			switch {
			case r.Error != "":
				result.Failed++
			case r.Skipped:
				result.Skipped++
			default:
				result.Downloaded++
			}
		}
	}

	return result, nil
}
