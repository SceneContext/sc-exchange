// Package rtd implements the SceneContext Real-Time Data PBS module.
//
// Enriches every CTV/OTT bid request with contextual segments from sc-vespa,
// written into content.data[] (OpenRTB 2.6) — available to all demand adapters
// automatically. Partner-specific adapters (PubMatic, Equativ) additionally
// map this into their proprietary schemas.
//
// The module is fail-open: any error or timeout leaves the request unchanged
// and the auction proceeds unenriched.
//
// Registered as "scenecontext/rtd" in the PBS module registry, consistent
// with the Prebid RTD ecosystem (scope3/rtd, etc.).
//
// Future sc-exchange modules (SCID resolver, ACR) follow this same
// Builder + HandleProcessedAuctionHook pattern.
package rtd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/prebid/prebid-server/v4/hooks/hookanalytics"
	"github.com/prebid/prebid-server/v4/hooks/hookstage"
	"github.com/prebid/prebid-server/v4/logger"
	"github.com/prebid/prebid-server/v4/modules/moduledeps"
	"github.com/prebid/prebid-server/v4/util/jsonutil"
)

const (
	moduleVendor       = "scenecontext"
	moduleName         = "rtd"
	sceneContextSource = "scenecontext.io"
	sceneContextName   = "SceneContext"
	defaultTimeoutMs   = 8
)

// Ensure Module implements the ProcessedAuctionRequest hook interface.
var _ hookstage.ProcessedAuctionRequest = (*Module)(nil)

// Config holds module configuration loaded from PBS config YAML.
//
// Example PBS config:
//
//	hooks:
//	  modules:
//	    scenecontext:
//	      rtd:
//	        endpoint: "http://sc-vespa:8000/bid"
//	        timeout_ms: 8
//	        enabled: true
type Config struct {
	Endpoint  string `json:"endpoint"`
	TimeoutMs int    `json:"timeout_ms"`
	Enabled   bool   `json:"enabled"`
}

// Module is the SceneContext RTD PBS module.
type Module struct {
	cfg        Config
	httpClient *http.Client
}

// Builder is the PBS entry point — called once at startup to initialise the module.
func Builder(config json.RawMessage, deps moduledeps.ModuleDeps) (interface{}, error) {
	var cfg Config
	if err := jsonutil.Unmarshal(config, &cfg); err != nil {
		return nil, fmt.Errorf("scenecontext/rtd: failed to unmarshal config: %w", err)
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("scenecontext/rtd: endpoint is required")
	}
	if cfg.TimeoutMs == 0 {
		cfg.TimeoutMs = defaultTimeoutMs
	}

	return &Module{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout:   time.Duration(cfg.TimeoutMs) * time.Millisecond,
			Transport: deps.HTTPClient.Transport, // reuse PBS shared transport (connection pooling)
		},
	}, nil
}

// ---------------------------------------------------------------------------
// PBS hook — ProcessedAuctionRequest stage
// ---------------------------------------------------------------------------

// HandleProcessedAuctionHook enriches the bid request with contextual CTV
// segments from sc-vespa. Called once per auction after request validation.
func (m *Module) HandleProcessedAuctionHook(
	ctx context.Context,
	_ hookstage.ModuleInvocationContext,
	payload hookstage.ProcessedAuctionRequestPayload,
) (hookstage.HookResult[hookstage.ProcessedAuctionRequestPayload], error) {
	var result hookstage.HookResult[hookstage.ProcessedAuctionRequestPayload]

	if !m.cfg.Enabled {
		return result, nil
	}

	bidReq := payload.Request.BidRequest
	if bidReq == nil {
		return result, nil
	}

	if !hasContentMetadata(bidReq) {
		return result, nil // no content signal — skip enrichment
	}

	segments, err := m.fetchSegments(ctx, bidReq)
	if err != nil {
		// fail-open: log, emit analytics tag, continue unenriched
		logger.Warnf("[scenecontext/rtd] fetch failed: %v", err)
		result.AnalyticsTags = errorTag("fetch_segments", err)
		return result, nil
	}
	if len(segments) == 0 {
		return result, nil // 204 from sc-vespa — cache miss, async processing
	}

	// Inject segments via PBS mutation (applied atomically by PBS)
	result.ChangeSet.AddMutation(
		func(p hookstage.ProcessedAuctionRequestPayload) (hookstage.ProcessedAuctionRequestPayload, error) {
			injectSegments(p.Request.BidRequest, segments)
			return p, nil
		},
		hookstage.MutationUpdate,
		"bidrequest", "app", "content", "data",
	)

	return result, nil
}

// ---------------------------------------------------------------------------
// sc-vespa call
// ---------------------------------------------------------------------------

type vespaResponse struct {
	Segments []string `json:"segments"`
}

func (m *Module) fetchSegments(ctx context.Context, bidReq *openrtb2.BidRequest) ([]string, error) {
	body, err := jsonutil.Marshal(bidReq)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return nil, nil // cache miss — sc-vespa processing async
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sc-vespa status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	var vr vespaResponse
	if err := json.Unmarshal(data, &vr); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	return vr.Segments, nil
}

// ---------------------------------------------------------------------------
// OpenRTB 2.6 injection — content.data[]
// ---------------------------------------------------------------------------

// injectSegments writes sc-vespa segments into content.data[] on the bid
// request. Handles app.content and site.content.
// Idempotent — replaces any existing SceneContext entry.
func injectSegments(bidReq *openrtb2.BidRequest, segments []string) {
	entry := buildDataEntry(segments)

	if bidReq.App != nil && bidReq.App.Content != nil {
		bidReq.App.Content.Data = upsertData(bidReq.App.Content.Data, entry)
		return
	}
	if bidReq.Site != nil && bidReq.Site.Content != nil {
		bidReq.Site.Content.Data = upsertData(bidReq.Site.Content.Data, entry)
	}
}

func buildDataEntry(segments []string) openrtb2.Data {
	segs := make([]openrtb2.Segment, len(segments))
	for i, s := range segments {
		segs[i] = openrtb2.Segment{ID: s}
	}
	return openrtb2.Data{
		ID:      sceneContextSource,
		Name:    sceneContextName,
		Segment: segs,
	}
}

// upsertData replaces any existing SceneContext entry, preserving all others.
func upsertData(existing []openrtb2.Data, entry openrtb2.Data) []openrtb2.Data {
	result := existing[:0:len(existing)]
	for _, d := range existing {
		if d.ID != sceneContextSource {
			result = append(result, d)
		}
	}
	return append(result, entry)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// hasContentMetadata returns true if the request carries any content signal
// that sc-vespa can use for enrichment.
func hasContentMetadata(bidReq *openrtb2.BidRequest) bool {
	if bidReq.App != nil && bidReq.App.Content != nil {
		c := bidReq.App.Content
		return c.Title != "" || c.ID != "" || c.Genre != ""
	}
	if bidReq.Site != nil && bidReq.Site.Content != nil {
		c := bidReq.Site.Content
		return c.Title != "" || c.ID != "" || c.Genre != ""
	}
	return false
}

func errorTag(activity string, err error) hookanalytics.Analytics {
	return hookanalytics.Analytics{
		Activities: []hookanalytics.Activity{{
			Name:   moduleVendor + "/" + moduleName + "." + activity,
			Status: hookanalytics.ActivityStatusError,
			Results: []hookanalytics.Result{{
				Status: hookanalytics.ResultStatusError,
				Values: map[string]interface{}{"error": err.Error()},
			}},
		}},
	}
}
