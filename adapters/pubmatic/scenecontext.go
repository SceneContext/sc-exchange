package pubmatic

// scenecontext.go — maps SceneContext RTD segments from content.data[]
// into PubMatic's dctr (key_val) keyword targeting format.
//
// After the scenecontext/rtd hook runs, segments live in:
//   request.App.Content.Data[] or request.Site.Content.Data[]
//
// PubMatic reads targeting keywords via the dctr field:
//   imp.ext.key_val = "sc_segment=SC_GENRE_CRIME_001|sc_segment=SC_MOOD_TENSE"
//
// This file is the only sc-exchange-specific change to the PubMatic adapter.

import (
	"strings"

	"github.com/prebid/openrtb/v20/openrtb2"
)

const (
	sceneContextDataSource = "scenecontext.io"
	sceneContextDctrKey    = "sc_segment"
)

// extractSceneContextSegments reads SceneContext segments from
// content.data[] — checks app.content first, then site.content.
func extractSceneContextSegments(request *openrtb2.BidRequest) []string {
	var data []openrtb2.Data

	if request.App != nil && request.App.Content != nil {
		data = request.App.Content.Data
	} else if request.Site != nil && request.Site.Content != nil {
		data = request.Site.Content.Data
	}

	for _, d := range data {
		if d.ID == sceneContextDataSource {
			segs := make([]string, 0, len(d.Segment))
			for _, s := range d.Segment {
				if s.ID != "" {
					segs = append(segs, s.ID)
				}
			}
			return segs
		}
	}
	return nil
}

// appendSceneContextDctr appends SceneContext segments to an existing dctr
// string in PubMatic's pipe-separated key=value format.
//
// Example output:
//   "sc_segment=SC_GENRE_CRIME_001|sc_segment=SC_MOOD_TENSE"
//
// If dctr already has values they are preserved:
//   "existing=val|sc_segment=SC_GENRE_CRIME_001"
func appendSceneContextDctr(existing string, segments []string) string {
	if len(segments) == 0 {
		return existing
	}

	var b strings.Builder
	if existing != "" {
		b.WriteString(existing)
		b.WriteByte('|')
	}
	for i, seg := range segments {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(sceneContextDctrKey)
		b.WriteByte('=')
		b.WriteString(seg)
	}
	return b.String()
}
