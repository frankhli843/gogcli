package cmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/api/calendar/v3"

	"github.com/openclaw/gogcli/internal/app"
	"github.com/openclaw/gogcli/internal/zoom"
)

func TestParseZoomMeetingSettings(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `{"auto_recording":"none"}`,
		`{"auto_recording":"invalid","auto_start_meeting_summary":false,"auto_start_ai_companion_questions":false}`,
		`{"auto_recording":"none","auto_start_meeting_summary":null,"auto_start_ai_companion_questions":false}`,
		`{"auto_recording":"none","auto_start_meeting_summary":false,"auto_start_ai_companion_questions":false,"typo":false}`,
		`{"auto_recording":"none","auto_start_meeting_summary":false,"auto_start_ai_companion_questions":false} {}`,
	} {
		if _, err := parseZoomMeetingSettings(raw); err == nil {
			t.Errorf("accepted incomplete or malformed settings: %s", raw)
		}
	}
	for _, mode := range []string{"none", "cloud"} {
		settings, err := parseZoomMeetingSettings(`{"auto_recording":"` + mode + `","auto_start_meeting_summary":false,"auto_start_ai_companion_questions":false}`)
		if err != nil {
			t.Fatal(err)
		}
		if settings.AutoRecording != mode || settings.AutoStartMeetingSummary || settings.AutoStartAICompanionQuestions {
			t.Fatalf("wrong settings: %+v", settings)
		}
	}
	settings, err := parseZoomMeetingSettings("")
	if err != nil || settings != nil {
		t.Fatal("omitted settings must remain unset")
	}
}

type explicitSettingsZoomClient struct {
	fakeZoomCalendarClient
	request   zoom.CreateMeetingRequest
	updatedID string
	updated   *zoom.MeetingSettings
}

func (f *explicitSettingsZoomClient) CreateMeeting(ctx context.Context, user string, request zoom.CreateMeetingRequest) (*zoom.Meeting, error) {
	f.request = request
	return f.fakeZoomCalendarClient.CreateMeeting(ctx, user, request)
}
func (f *explicitSettingsZoomClient) UpdateMeetingSettings(_ context.Context, id string, settings *zoom.MeetingSettings) error {
	f.updatedID, f.updated = id, settings
	return nil
}

func TestCalendarExplicitZoomSettingsCreateAndExistingUpdate(t *testing.T) {
	raw := `{"auto_recording":"none","auto_start_meeting_summary":false,"auto_start_ai_companion_questions":false}`
	for _, update := range []bool{false, true} {
		t.Run(fmtSprintBool(update), func(t *testing.T) {
			client := &explicitSettingsZoomClient{}
			server := httptest.NewServer(withPrimaryCalendar(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				path := strings.TrimPrefix(r.URL.Path, "/calendar/v3")
				if r.Method == http.MethodGet && path == "/calendars/cal@example.com/events/ev" {
					_ = json.NewEncoder(w).Encode(zoomEventJSON("ev", "1001"))
					return
				}
				if r.Method == http.MethodPost || r.Method == http.MethodPatch {
					var event calendar.Event
					_ = json.NewDecoder(r.Body).Decode(&event)
					_ = json.NewEncoder(w).Encode(event)
					return
				}
				http.NotFound(w, r)
			})))
			defer server.Close()
			svc := newCalendarServiceFromZoomTestServer(t, context.Background(), server)
			ctx, _ := newCalendarTestJSONContext(t, svc)
			ctx = withTestRuntime(ctx, func(runtime *app.Runtime) {
				runtime.Services.Zoom = func(context.Context, string) (app.ZoomMeetingClient, error) { return client, nil }
			})
			var err error
			if update {
				err = runKong(t, &CalendarUpdateCmd{}, []string{"cal@example.com", "ev", "--zoom-settings", raw}, ctx, &RootFlags{Account: "a@b.com"})
				if client.created != 0 || client.updatedID != "1001" || client.updated == nil || client.updated.AutoRecording != "none" {
					t.Fatalf("did not patch same existing Zoom meeting: %+v", client)
				}
			} else {
				err = runKong(t, &CalendarCreateCmd{}, []string{"cal@example.com", "--summary", "Policy test", "--from", "2026-11-03T10:00:00Z", "--to", "2026-11-03T10:30:00Z", "--with-zoom", "--zoom-settings", raw}, ctx, &RootFlags{Account: "a@b.com"})
				if client.created != 1 || client.request.Settings == nil || client.request.Settings.AutoRecording != "none" {
					t.Fatalf("settings lost before creation: %+v", client)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
func fmtSprintBool(v bool) string {
	if v {
		return "update"
	}
	return "create"
}

func TestZoomMeetingCommandsDryRunCarriesExplicitSettings(t *testing.T) {
	raw := `{"auto_recording":"none","auto_start_meeting_summary":false,"auto_start_ai_companion_questions":false}`
	for _, args := range [][]string{
		{"--json", "--dry-run", "zoom", "meeting", "create", "--topic", "Policy test", "--from", "2026-11-03T10:00:00Z", "--zoom-settings", raw},
		{"--json", "--dry-run", "zoom", "meeting", "update", "123", "--zoom-settings", raw},
	} {
		result := executeWithTestRuntime(t, args, &app.Runtime{Services: app.Services{
			Zoom: func(context.Context, string) (app.ZoomMeetingClient, error) {
				t.Fatal("dry-run must not access Zoom")
				return nil, nil
			},
		}})
		if result.err != nil {
			t.Fatal(result.err)
		}
		var output struct {
			DryRun  bool `json:"dry_run"`
			Request struct {
				Settings *zoom.MeetingSettings `json:"settings"`
			} `json:"request"`
		}
		if err := json.Unmarshal([]byte(result.stdout), &output); err != nil {
			t.Fatal(err)
		}
		if !output.DryRun || output.Request.Settings == nil || output.Request.Settings.AutoRecording != "none" {
			t.Fatalf("missing explicit dry-run settings: %s", result.stdout)
		}
	}
}
