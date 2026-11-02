package cmd

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"

	"github.com/openclaw/gogcli/internal/outfmt"
	"github.com/openclaw/gogcli/internal/zoom"
)

type zoomSettingsContextKey struct{}

func withExplicitZoomSettings(ctx context.Context, settings *zoom.MeetingSettings) context.Context {
	return context.WithValue(ctx, zoomSettingsContextKey{}, settings)
}

func explicitZoomSettings(ctx context.Context) *zoom.MeetingSettings {
	settings, _ := ctx.Value(zoomSettingsContextKey{}).(*zoom.MeetingSettings)
	return settings
}

func parseZoomMeetingSettings(raw string) (*zoom.MeetingSettings, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var fields struct {
		AutoRecording                 *string `json:"auto_recording"`
		AutoStartMeetingSummary       *bool   `json:"auto_start_meeting_summary"`
		AutoStartAICompanionQuestions *bool   `json:"auto_start_ai_companion_questions"`
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fields); err != nil {
		return nil, usage("invalid --zoom-settings JSON: " + err.Error())
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, usage("--zoom-settings must contain exactly one JSON object")
	}
	if fields.AutoRecording == nil || fields.AutoStartMeetingSummary == nil || fields.AutoStartAICompanionQuestions == nil {
		return nil, usage("--zoom-settings requires auto_recording, auto_start_meeting_summary and auto_start_ai_companion_questions")
	}
	switch *fields.AutoRecording {
	case "none", "cloud", "local":
	default:
		return nil, usage("auto_recording must be none, cloud or local")
	}
	return &zoom.MeetingSettings{AutoRecording: *fields.AutoRecording, AutoStartMeetingSummary: *fields.AutoStartMeetingSummary, AutoStartAICompanionQuestions: *fields.AutoStartAICompanionQuestions}, nil
}

func zoomSettingsDryRun(request map[string]any, settings *zoom.MeetingSettings) map[string]any {
	if settings != nil {
		request["zoom_settings"] = settings
	}
	return request
}

func updateZoomMeetingSettings(ctx context.Context, id string, settings *zoom.MeetingSettings) error {
	client, err := zoomMeetingClient(ctx, "")
	if err != nil {
		return err
	}
	updater, ok := client.(interface {
		UpdateMeetingSettings(context.Context, string, *zoom.MeetingSettings) error
	})
	if !ok {
		return usage("Zoom client does not support explicit settings updates")
	}
	return updater.UpdateMeetingSettings(ctx, id, settings)
}

type ZoomMeetingCmd struct {
	Create ZoomMeetingCreateCmd `cmd:"" help:"Create a Zoom meeting"`
	Get    ZoomMeetingGetCmd    `cmd:"" help:"Read actual Zoom meeting settings"`
	Update ZoomMeetingUpdateCmd `cmd:"" help:"Set explicit per-meeting Zoom recording settings"`
	Delete ZoomMeetingDeleteCmd `cmd:"" help:"Delete a Zoom meeting"`
}

type ZoomMeetingCreateCmd struct {
	Topic        string `name:"topic" help:"Meeting topic"`
	From         string `name:"from" help:"Start time in RFC3339"`
	Duration     int    `name:"duration" help:"Duration in minutes" default:"30"`
	Timezone     string `name:"timezone" help:"IANA timezone" default:"UTC"`
	ZoomSettings string `name:"zoom-settings" help:"Explicit settings JSON; omitted preserves account defaults"`
}

func (c *ZoomMeetingCreateCmd) Run(ctx context.Context, flags *RootFlags) error {
	settings, err := parseZoomMeetingSettings(c.ZoomSettings)
	if err != nil {
		return err
	}
	request := zoom.CreateMeetingRequest{Topic: c.Topic, Duration: c.Duration, Timezone: c.Timezone, Settings: settings}
	if c.From != "" {
		request.StartTime, err = time.Parse(time.RFC3339, c.From)
		if err != nil {
			return usage("--from must be RFC3339")
		}
	}
	if err := dryRunExit(ctx, flags, "zoom.meeting.create", map[string]any{"topic": c.Topic, "start_time": c.From, "duration": c.Duration, "timezone": c.Timezone, "settings": settings}); err != nil {
		return err
	}
	client, err := zoomMeetingClient(ctx, "")
	if err != nil {
		return err
	}
	meeting, err := client.CreateMeeting(ctx, "me", request)
	if err != nil {
		return err
	}
	return writeZoomMeeting(ctx, meeting)
}

type ZoomMeetingGetCmd struct {
	MeetingID string `arg:"" name:"meetingId" help:"Zoom meeting ID"`
}

func (c *ZoomMeetingGetCmd) Run(ctx context.Context) error {
	client, err := zoomMeetingClient(ctx, "")
	if err != nil {
		return err
	}
	reader, ok := client.(interface {
		GetMeeting(context.Context, string) (*zoom.Meeting, error)
	})
	if !ok {
		return usage("Zoom client does not support meeting reads")
	}
	meeting, err := reader.GetMeeting(ctx, c.MeetingID)
	if err != nil {
		return err
	}
	return writeZoomMeeting(ctx, meeting)
}

type ZoomMeetingUpdateCmd struct {
	MeetingID    string `arg:"" name:"meetingId" help:"Zoom meeting ID"`
	ZoomSettings string `name:"zoom-settings" required:"" help:"Explicit settings JSON"`
}

func (c *ZoomMeetingUpdateCmd) Run(ctx context.Context, flags *RootFlags) error {
	settings, err := parseZoomMeetingSettings(c.ZoomSettings)
	if err != nil {
		return err
	}
	if settings == nil {
		return usage("--zoom-settings is required")
	}
	if err := dryRunExit(ctx, flags, "zoom.meeting.update", map[string]any{"meeting_id": c.MeetingID, "settings": settings}); err != nil {
		return err
	}
	if err := updateZoomMeetingSettings(ctx, c.MeetingID, settings); err != nil {
		return err
	}
	return outfmt.WriteJSON(ctx, stdoutWriter(ctx), map[string]any{"updated": true, "meeting_id": c.MeetingID, "settings": settings})
}

type ZoomMeetingDeleteCmd struct {
	MeetingID string `arg:"" name:"meetingId" help:"Zoom meeting ID"`
}

func (c *ZoomMeetingDeleteCmd) Run(ctx context.Context, flags *RootFlags) error {
	if err := dryRunExit(ctx, flags, "zoom.meeting.delete", map[string]any{"meeting_id": c.MeetingID}); err != nil {
		return err
	}
	client, err := zoomMeetingClient(ctx, "")
	if err != nil {
		return err
	}
	if err := client.DeleteMeeting(ctx, c.MeetingID); err != nil {
		return err
	}
	return outfmt.WriteJSON(ctx, stdoutWriter(ctx), map[string]any{"deleted": true, "meeting_id": c.MeetingID})
}

func writeZoomMeeting(ctx context.Context, meeting *zoom.Meeting) error {
	if meeting != nil {
		meeting.StartURL = ""
		if !zoom.IncludePasswordsFromEnv() {
			meeting.Password = ""
			meeting.JoinURL = zoom.RedactZoomURL(meeting.JoinURL)
		}
	}
	return outfmt.WriteJSON(ctx, stdoutWriter(ctx), meeting)
}
