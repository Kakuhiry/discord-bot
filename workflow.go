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

	// Fan out: kick off every download concurrently, then wait on all of them.
	futures := make([]workflow.Future, len(scanResult.URLs))
	for i, url := range scanResult.URLs {
		futures[i] = workflow.ExecuteActivity(downloadCtx, DownloadVideoActivity, DownloadVideoInput{
			URL:       url,
			ChannelID: input.ChannelID,
		})
	}

	result := DownloadChannelResult{Found: len(scanResult.URLs)}
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

	return result, nil
}
