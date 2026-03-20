package smartadserver

// scenecontext.go — SceneContext RTD integration notes for Equativ (SmartAdServer).
//
// No code changes are required in this adapter.
//
// The scenecontext/rtd PBS hook injects segments into content.data[] before
// MakeRequests runs. Because smartRequest := *request shares the App pointer,
// the enriched content.data[] is automatically included in the JSON sent to
// Equativ's auction server.
//
// Equativ reads content.data[] natively (OpenRTB 2.6 compliant) and maps
// SceneContext segments (e.g. SC_GENRE_CRIME_001) to Deal IDs configured in
// their platform via the RTDS segment taxonomy.
//
// Wire format Equativ receives in app.content.data[]:
//
//	{
//	  "id": "scenecontext.io",
//	  "name": "SceneContext",
//	  "segment": [
//	    {"id": "SC_GENRE_CRIME_001"},
//	    {"id": "SC_MOOD_TENSE"}
//	  ]
//	}
//
// No adapter patch needed. Segments flow through the standard OpenRTB path.
