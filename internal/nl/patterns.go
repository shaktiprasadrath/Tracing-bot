package nl

import "regexp"

// DefaultPatterns is F10 §4.4's golden pattern table: >= 4 representative
// surface forms per IntentKind (FR-F10-9), matched entirely offline.
// Ordered so more specific phrasings (e.g. "explain trace <id>") outrank
// generic ones; Priority breaks ties within Interpret's confidence scoring.
func DefaultPatterns() []Rule {
	return []Rule{
		// IntentExplainTrace — checked ahead of TraceSearch: a literal trace ID
		// present in the text is the strongest possible signal.
		{Intent: IntentExplainTrace, Pattern: regexp.MustCompile(`(?i)\bexplain\s+trace\b`), Priority: 9},
		{Intent: IntentExplainTrace, Pattern: regexp.MustCompile(`(?i)\bwhat happened in trace\b`), Priority: 9},
		{Intent: IntentExplainTrace, Pattern: regexp.MustCompile(`(?i)\bwalk me through trace\b`), Priority: 9},
		{Intent: IntentExplainTrace, Pattern: regexp.MustCompile(`(?i)\bwhy did trace\b.*\bfail\b`), Priority: 9},
		{Intent: IntentExplainTrace, Pattern: regexp.MustCompile(`(?i)\btrace\s+[0-9a-fA-F]{32}\b`), Priority: 6},

		// IntentTraceSearch
		{Intent: IntentTraceSearch, Pattern: regexp.MustCompile(`(?i)\bfind\s+traces\b`), Priority: 5},
		{Intent: IntentTraceSearch, Pattern: regexp.MustCompile(`(?i)\bshow\s+me\s+traces\b`), Priority: 5},
		{Intent: IntentTraceSearch, Pattern: regexp.MustCompile(`(?i)\bsearch\s+traces\b`), Priority: 5},
		{Intent: IntentTraceSearch, Pattern: regexp.MustCompile(`(?i)\b(list|show)\s+(slow|error(ing)?)\s+traces\b`), Priority: 5},
		{Intent: IntentTraceSearch, Pattern: regexp.MustCompile(`(?i)\btraces\s+(with|for|in)\b`), Priority: 3},

		// IntentServiceHealth
		{Intent: IntentServiceHealth, Pattern: regexp.MustCompile(`(?i)\bhow\s+is\b.*\bdoing\b`), Priority: 6},
		{Intent: IntentServiceHealth, Pattern: regexp.MustCompile(`(?i)\bhealth\s+of\b`), Priority: 6},
		{Intent: IntentServiceHealth, Pattern: regexp.MustCompile(`(?i)\bis\b.*\bhealthy\b`), Priority: 6},
		{Intent: IntentServiceHealth, Pattern: regexp.MustCompile(`(?i)\bstatus\s+of\b.*\bservice\b`), Priority: 6},
		{Intent: IntentServiceHealth, Pattern: regexp.MustCompile(`(?i)\berror\s+rate\s+for\b`), Priority: 6},
		{Intent: IntentServiceHealth, Pattern: regexp.MustCompile(`(?i)\bwhat\s+is\s+the\s+error\s+rate\b`), Priority: 6},

		// IntentTopologyQuestion
		{Intent: IntentTopologyQuestion, Pattern: regexp.MustCompile(`(?i)\bwhat\s+calls\b`), Priority: 6},
		{Intent: IntentTopologyQuestion, Pattern: regexp.MustCompile(`(?i)\bwho\s+depends\s+on\b`), Priority: 6},
		{Intent: IntentTopologyQuestion, Pattern: regexp.MustCompile(`(?i)\btopology\s+for\b`), Priority: 6},
		{Intent: IntentTopologyQuestion, Pattern: regexp.MustCompile(`(?i)\bservice\s+map\s+for\b`), Priority: 6},
		{Intent: IntentTopologyQuestion, Pattern: regexp.MustCompile(`(?i)\bwhat\s+does\b.*\bcall\b`), Priority: 6},
		{Intent: IntentTopologyQuestion, Pattern: regexp.MustCompile(`(?i)\bshow\s+topology\b`), Priority: 6},

		// IntentIncidentStatus
		{Intent: IntentIncidentStatus, Pattern: regexp.MustCompile(`(?i)\bactive\s+incidents\b`), Priority: 6},
		{Intent: IntentIncidentStatus, Pattern: regexp.MustCompile(`(?i)\bopen\s+incidents\b`), Priority: 6},
		{Intent: IntentIncidentStatus, Pattern: regexp.MustCompile(`(?i)\bincidents?\s+(are\s+)?(currently\s+)?active\b`), Priority: 6},
		{Intent: IntentIncidentStatus, Pattern: regexp.MustCompile(`(?i)\bany\s+incidents\b`), Priority: 6},
		{Intent: IntentIncidentStatus, Pattern: regexp.MustCompile(`(?i)\bincident\s+status\b`), Priority: 6},
		{Intent: IntentIncidentStatus, Pattern: regexp.MustCompile(`(?i)\blist\s+.*\bincidents\b`), Priority: 6},

		// IntentInvestigationAsk
		{Intent: IntentInvestigationAsk, Pattern: regexp.MustCompile(`(?i)\b(what\s+did|find(ings)?\s+for)\s+investigation\b`), Priority: 7},
		{Intent: IntentInvestigationAsk, Pattern: regexp.MustCompile(`(?i)\bstatus\s+of\s+investigation\b`), Priority: 7},
		{Intent: IntentInvestigationAsk, Pattern: regexp.MustCompile(`(?i)\btell\s+me\s+about\s+investigation\b`), Priority: 7},
		{Intent: IntentInvestigationAsk, Pattern: regexp.MustCompile(`(?i)\bfindings\s+for\s+investigation\b`), Priority: 7},
		{Intent: IntentInvestigationAsk, Pattern: regexp.MustCompile(`(?i)\binvestigation\s+inv-\S+\b`), Priority: 4},

		// IntentMemoryLookup
		{Intent: IntentMemoryLookup, Pattern: regexp.MustCompile(`(?i)\bhave\s+we\s+seen\s+this\s+before\b`), Priority: 6},
		{Intent: IntentMemoryLookup, Pattern: regexp.MustCompile(`(?i)\bsimilar\s+incidents?\b`), Priority: 6},
		{Intent: IntentMemoryLookup, Pattern: regexp.MustCompile(`(?i)\brecall\b.*\bsimilar\b`), Priority: 6},
		{Intent: IntentMemoryLookup, Pattern: regexp.MustCompile(`(?i)\bpast\s+investigations?\s+like\b`), Priority: 6},
		{Intent: IntentMemoryLookup, Pattern: regexp.MustCompile(`(?i)\bseen\s+this\s+error\s+before\b`), Priority: 6},

		// IntentStartInvestigation
		{Intent: IntentStartInvestigation, Pattern: regexp.MustCompile(`(?i)\bwhy\s+is\b.*\b(slow|down|failing|erroring)\b`), Priority: 7},
		{Intent: IntentStartInvestigation, Pattern: regexp.MustCompile(`(?i)\binvestigate\b`), Priority: 7},
		{Intent: IntentStartInvestigation, Pattern: regexp.MustCompile(`(?i)\bstart\s+an?\s+investigation\b`), Priority: 7},
		{Intent: IntentStartInvestigation, Pattern: regexp.MustCompile(`(?i)\broot\s+cause\s+of\b`), Priority: 7},
		{Intent: IntentStartInvestigation, Pattern: regexp.MustCompile(`(?i)\bfind\s+the\s+root\s+cause\b`), Priority: 7},

		// IntentCompareWindows
		{Intent: IntentCompareWindows, Pattern: regexp.MustCompile(`(?i)\bcompare\b`), Priority: 6},
		{Intent: IntentCompareWindows, Pattern: regexp.MustCompile(`(?i)\bdiff\s+between\b`), Priority: 6},
		{Intent: IntentCompareWindows, Pattern: regexp.MustCompile(`(?i)\bwhat\s+changed\s+since\b`), Priority: 6},
		{Intent: IntentCompareWindows, Pattern: regexp.MustCompile(`(?i)\bbefore\s+and\s+after\b`), Priority: 6},

		// IntentRemediate
		{Intent: IntentRemediate, Pattern: regexp.MustCompile(`(?i)\brestart\b`), Priority: 6},
		{Intent: IntentRemediate, Pattern: regexp.MustCompile(`(?i)\brollback\b`), Priority: 6},
		{Intent: IntentRemediate, Pattern: regexp.MustCompile(`(?i)\bscale\b.*\breplicas?\b`), Priority: 6},
		{Intent: IntentRemediate, Pattern: regexp.MustCompile(`(?i)\btoggle\b.*\bfeature\s+flag\b`), Priority: 6},
		{Intent: IntentRemediate, Pattern: regexp.MustCompile(`(?i)\bremediate\b`), Priority: 6},
	}
}
