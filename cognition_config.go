package surriti

type CognitionConfig struct {
	Enabled                       bool
	IdleSeconds                   float64
	BatchThreshold                int
	DecayAwareRecall              bool
	MemorySilenceEnabled          bool
	MemorySilenceMinAgeDays       float64
	MemorySilenceActivation       float64
	MemorySilenceSweepLimit       int
	ConsolidationThreshold        int
	ConsolidationMinSpanDays      float64
	DomainLabelingEveryNPasses    int
	AffectExtraction              bool
	BeliefExtraction              bool
	TraitSynthesis                bool
	GoalSynthesis                 bool
	ProceduralSynthesis           bool
	Consolidation                 bool
	StagnantConsolidation         bool
	StagnantMinEdgesPerSummary    int
	StagnantMaxEdgesPerPass       int
	Prediction                    bool
	SelfAwareness                 bool
	MaxEpisodesPerPass            int
	MaxConcurrentGroups           int
	DecayHalfLifeDays             map[string]float64
}

func DefaultCognitionConfig() CognitionConfig {
	return CognitionConfig{
		Enabled:true, IdleSeconds:8, BatchThreshold:5, DecayAwareRecall:true,
		MemorySilenceEnabled:true, MemorySilenceMinAgeDays:30, MemorySilenceActivation:-3.5, MemorySilenceSweepLimit:500,
		ConsolidationThreshold:8, ConsolidationMinSpanDays:14, DomainLabelingEveryNPasses:3,
		AffectExtraction:true, BeliefExtraction:true, TraitSynthesis:true, GoalSynthesis:true,
		ProceduralSynthesis:true, Consolidation:true, StagnantConsolidation:true,
		StagnantMinEdgesPerSummary:5, StagnantMaxEdgesPerPass:120,
		Prediction:true, SelfAwareness:true, MaxEpisodesPerPass:32, MaxConcurrentGroups:4,
	}
}
