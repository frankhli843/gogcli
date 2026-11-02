package zoom

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExplicitMeetingSettingsRequestBodies(t *testing.T) {
	for _, mode := range []string{"none", "cloud", ""} {
		t.Run(mode, func(t *testing.T) {
			var bodies []map[string]json.RawMessage
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oauth/token" {
					_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "expires_in": 3600})
					return
				}
				if r.Method == http.MethodGet {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": 123, "settings": map[string]any{"auto_recording": mode, "auto_start_meeting_summary": false, "auto_start_ai_companion_questions": false}})
					return
				}
				var body map[string]json.RawMessage
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				bodies = append(bodies, body)
				if r.Method == http.MethodPatch {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"id": 123})
			}))
			defer server.Close()
			client, _ := newTestClient(t, server, "settings-"+mode, time.Now())
			var settings *MeetingSettings
			if mode != "" {
				settings = &MeetingSettings{AutoRecording: mode}
			}
			if _, err := client.CreateMeeting(context.Background(), "me", CreateMeetingRequest{Settings: settings}); err != nil {
				t.Fatal(err)
			}
			if mode == "" {
				if _, present := bodies[0]["settings"]; present {
					t.Fatal("omitted flag must preserve account-default behavior")
				}
				return
			}
			if err := client.UpdateMeetingSettings(context.Background(), "123", settings); err != nil {
				t.Fatal(err)
			}
			for _, body := range bodies {
				var actual map[string]any
				if err := json.Unmarshal(body["settings"], &actual); err != nil {
					t.Fatal(err)
				}
				if actual["auto_recording"] != mode || actual["auto_start_meeting_summary"] != false || actual["auto_start_ai_companion_questions"] != false || len(actual) != 3 {
					t.Fatalf("settings do not explicitly override account defaults: %s", body["settings"])
				}
			}
			meeting, err := client.GetMeeting(context.Background(), "123")
			if err != nil {
				t.Fatal(err)
			}
			if meeting.Settings == nil || meeting.Settings.AutoRecording != mode {
				t.Fatalf("settings readback lost: %+v", meeting)
			}
		})
	}
}
