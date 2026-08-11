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

type DownloadChannelResult struct {
	Found      int
	Downloaded int
	Skipped    int
	Failed     int
}

// DownloadChannelWorkflow scans a Discord channel's full message history for
// video links and downloads every one of them. Safe to re-run: links already
// fetched by a previous run are skipped via yt-dlp's download archive.
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

	downloadCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	})

	// Downloads run in batches rather than all at once: yt-dlp (plus ffmpeg
	// for merging/transcoding) is heavy per-process, and this worker shares
	// its node with etcd and Longhorn storage duty -- fanning out everything
	// found in one go is enough to starve the node.
	const batchSize = 5
	result := DownloadChannelResult{Found: len(scanResult.URLs)}

	for start := 0; start < len(scanResult.URLs); start += batchSize {
		end := start + batchSize
		if end > len(scanResult.URLs) {
			end = len(scanResult.URLs)
		}
		batch := scanResult.URLs[start:end]

		futures := make([]workflow.Future, len(batch))
		for i, url := range batch {
			futures[i] = workflow.ExecuteActivity(downloadCtx, DownloadVideoActivity, DownloadVideoInput{
				URL:       url,
				ChannelID: input.ChannelID,
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
