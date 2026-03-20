package pubmatic

import (
	"testing"

	"github.com/prebid/openrtb/v20/openrtb2"
	"github.com/stretchr/testify/assert"
)

func TestExtractSceneContextSegments_AppContent(t *testing.T) {
	req := &openrtb2.BidRequest{
		App: &openrtb2.App{
			Content: &openrtb2.Content{
				Data: []openrtb2.Data{
					{
						ID:   "scenecontext.io",
						Name: "SceneContext",
						Segment: []openrtb2.Segment{
							{ID: "SC_GENRE_CRIME_001"},
							{ID: "SC_MOOD_TENSE"},
						},
					},
				},
			},
		},
	}

	segs := extractSceneContextSegments(req)
	assert.Equal(t, []string{"SC_GENRE_CRIME_001", "SC_MOOD_TENSE"}, segs)
}

func TestExtractSceneContextSegments_SiteContent(t *testing.T) {
	req := &openrtb2.BidRequest{
		Site: &openrtb2.Site{
			Content: &openrtb2.Content{
				Data: []openrtb2.Data{
					{ID: "scenecontext.io", Segment: []openrtb2.Segment{{ID: "SC_GENRE_NEWS_001"}}},
				},
			},
		},
	}

	segs := extractSceneContextSegments(req)
	assert.Equal(t, []string{"SC_GENRE_NEWS_001"}, segs)
}

func TestExtractSceneContextSegments_OtherProviderIgnored(t *testing.T) {
	req := &openrtb2.BidRequest{
		App: &openrtb2.App{
			Content: &openrtb2.Content{
				Data: []openrtb2.Data{
					{ID: "other-provider.com", Segment: []openrtb2.Segment{{ID: "OTHER_SEG"}}},
				},
			},
		},
	}

	segs := extractSceneContextSegments(req)
	assert.Empty(t, segs)
}

func TestExtractSceneContextSegments_NoContent(t *testing.T) {
	req := &openrtb2.BidRequest{App: &openrtb2.App{ID: "app-no-content"}}
	assert.Empty(t, extractSceneContextSegments(req))
}

func TestAppendSceneContextDctr_EmptyExisting(t *testing.T) {
	result := appendSceneContextDctr("", []string{"SC_GENRE_CRIME_001", "SC_MOOD_TENSE"})
	assert.Equal(t, "sc_segment=SC_GENRE_CRIME_001|sc_segment=SC_MOOD_TENSE", result)
}

func TestAppendSceneContextDctr_ExistingPreserved(t *testing.T) {
	result := appendSceneContextDctr("existing=val", []string{"SC_GENRE_CRIME_001"})
	assert.Equal(t, "existing=val|sc_segment=SC_GENRE_CRIME_001", result)
}

func TestAppendSceneContextDctr_NoSegments(t *testing.T) {
	result := appendSceneContextDctr("existing=val", nil)
	assert.Equal(t, "existing=val", result)
}
