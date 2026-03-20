package rtd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/modules/moduledeps"
	"github.com/prebid/prebid-server/v4/openrtb_ext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newModule(t *testing.T, serverURL string, timeoutMs int) *Module {
	t.Helper()
	cfg, _ := json.Marshal(Config{
		Endpoint:  serverURL + "/bid",
		TimeoutMs: timeoutMs,
		Enabled:   true,
	})
	m, err := Builder(cfg, moduledeps.ModuleDeps{HTTPClient: &http.Client{}})
	require.NoError(t, err)
	return m.(*Module)
}

func makePayload(app *openrtb2.App, site *openrtb2.Site) hookstage.ProcessedAuctionRequestPayload {
	return hookstage.ProcessedAuctionRequestPayload{
		Request: &openrtb_ext.RequestWrapper{
			BidRequest: &openrtb2.BidRequest{
				ID:   "test-req",
				App:  app,
				Site: site,
			},
		},
	}
}

func ctvApp(contentID, title, genre string) *openrtb2.App {
	return &openrtb2.App{
		ID:     "fubo-tv",
		Bundle: "com.fubo.tv",
		Content: &openrtb2.Content{
			ID:    contentID,
			Title: title,
			Genre: genre,
		},
	}
}

func vespaOK(segments []string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(vespaResponse{Segments: segments})
	}
}

func runHook(t *testing.T, m *Module, payload hookstage.ProcessedAuctionRequestPayload) hookstage.ProcessedAuctionRequestPayload {
	t.Helper()
	result, err := m.HandleProcessedAuctionHook(context.Background(), hookstage.ModuleInvocationContext{}, payload)
	require.NoError(t, err)
	// apply mutations as PBS would
	for _, mut := range result.ChangeSet.Mutations() {
		updated, err := mut.Apply(payload)
		require.NoError(t, err)
		payload = updated
	}
	return payload
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestModule_InjectsSegmentsIntoAppContent(t *testing.T) {
	srv := httptest.NewServer(vespaOK([]string{"SC_GENRE_CRIME_001", "SC_MOOD_TENSE"}))
	defer srv.Close()

	m := newModule(t, srv.URL, 50)
	payload := makePayload(ctvApp("B019DCHDZK", "NCIS New Orleans", "Drama"), nil)
	payload = runHook(t, m, payload)

	data := payload.Request.BidRequest.App.Content.Data
	require.Len(t, data, 1)
	assert.Equal(t, sceneContextSource, data[0].ID)
	assert.Equal(t, sceneContextName, data[0].Name)
	require.Len(t, data[0].Segment, 2)
	assert.Equal(t, "SC_GENRE_CRIME_001", data[0].Segment[0].ID)
	assert.Equal(t, "SC_MOOD_TENSE", data[0].Segment[1].ID)
}

func TestModule_InjectsSegmentsIntoSiteContent(t *testing.T) {
	srv := httptest.NewServer(vespaOK([]string{"SC_GENRE_NEWS_001"}))
	defer srv.Close()

	m := newModule(t, srv.URL, 50)
	payload := makePayload(nil, &openrtb2.Site{
		Domain: "news.example.com",
		Content: &openrtb2.Content{Title: "Evening News", Genre: "News"},
	})
	payload = runHook(t, m, payload)

	data := payload.Request.BidRequest.Site.Content.Data
	require.Len(t, data, 1)
	assert.Equal(t, "SC_GENRE_NEWS_001", data[0].Segment[0].ID)
}

func TestModule_FailOpen_OnTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		vespaOK([]string{"SC_GENRE_CRIME_001"})(w, r)
	}))
	defer srv.Close()

	m := newModule(t, srv.URL, 5) // 5ms timeout — server sleeps 100ms
	payload := makePayload(ctvApp("tt0903747", "Breaking Bad", "Crime"), nil)
	payload = runHook(t, m, payload)

	assert.Empty(t, payload.Request.BidRequest.App.Content.Data, "no data injected on timeout")
}

func TestModule_FailOpen_On204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	m := newModule(t, srv.URL, 50)
	payload := makePayload(ctvApp("", "Unknown Show", ""), nil)
	payload = runHook(t, m, payload)

	assert.Empty(t, payload.Request.BidRequest.App.Content.Data)
}

func TestModule_FailOpen_OnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m := newModule(t, srv.URL, 50)
	payload := makePayload(ctvApp("tt123", "Some Show", "Drama"), nil)
	payload = runHook(t, m, payload)

	assert.Empty(t, payload.Request.BidRequest.App.Content.Data)
}

func TestModule_Disabled_SkipsVespaCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	cfg, _ := json.Marshal(Config{Endpoint: srv.URL + "/bid", Enabled: false})
	mod, err := Builder(cfg, moduledeps.ModuleDeps{HTTPClient: &http.Client{}})
	require.NoError(t, err)

	payload := makePayload(ctvApp("tt123", "Some Show", "Drama"), nil)
	mod.(*Module).HandleProcessedAuctionHook(context.Background(), hookstage.ModuleInvocationContext{}, payload)

	assert.False(t, called, "sc-vespa must not be called when module is disabled")
}

func TestModule_NoContentMetadata_SkipsVespaCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	m := newModule(t, srv.URL, 50)
	payload := makePayload(&openrtb2.App{ID: "app-no-content"}, nil) // no content object
	m.HandleProcessedAuctionHook(context.Background(), hookstage.ModuleInvocationContext{}, payload)

	assert.False(t, called)
}

func TestModule_Idempotent_ReplacesExistingEntry(t *testing.T) {
	srv := httptest.NewServer(vespaOK([]string{"SC_GENRE_COMEDY_001"}))
	defer srv.Close()

	m := newModule(t, srv.URL, 50)
	payload := makePayload(ctvApp("tt0108778", "Friends", "Comedy"), nil)

	// pre-inject a stale SceneContext entry + an unrelated provider
	payload.Request.BidRequest.App.Content.Data = []openrtb2.Data{
		{ID: sceneContextSource, Name: sceneContextName, Segment: []openrtb2.Segment{{ID: "OLD_SEGMENT"}}},
		{ID: "other-provider.com", Name: "Other", Segment: []openrtb2.Segment{{ID: "OTHER_SEG"}}},
	}

	payload = runHook(t, m, payload)

	data := payload.Request.BidRequest.App.Content.Data
	require.Len(t, data, 2, "SceneContext entry replaced, other provider preserved")

	var scEntry *openrtb2.Data
	for i := range data {
		if data[i].ID == sceneContextSource {
			scEntry = &data[i]
		}
	}
	require.NotNil(t, scEntry)
	assert.Equal(t, "SC_GENRE_COMEDY_001", scEntry.Segment[0].ID, "stale segment replaced")
}

func TestBuilder_MissingEndpoint_ReturnsError(t *testing.T) {
	cfg, _ := json.Marshal(Config{Enabled: true}) // no endpoint
	_, err := Builder(cfg, moduledeps.ModuleDeps{HTTPClient: &http.Client{}})
	assert.ErrorContains(t, err, "endpoint is required")
}
