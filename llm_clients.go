package surriti

import "context"

type completionFunc func(context.Context, string, string) (string, error)

func providerExtract(ctx context.Context, complete completionFunc, req ExtractionRequest) (ExtractionResult, error) {
	raw, err := complete(ctx, ExtractionSystemPrompt, buildExtractionUser(req))
	if err != nil {
		return ExtractionResult{}, err
	}
	return parseExtractionJSON(raw)
}
func providerFindContradictions(ctx context.Context, complete completionFunc, req ContradictionRequest) ([]int, error) {
	if len(req.ExistingFacts) == 0 {
		return []int{}, nil
	}
	raw, err := complete(ctx, ContradictionSystemPrompt, buildContradictionUser(req))
	if err != nil {
		return nil, err
	}
	return parseContradictionsJSON(raw, len(req.ExistingFacts)), nil
}
func providerClassifyRelationFrame(ctx context.Context, complete completionFunc, req FrameClassificationRequest) (*RelationFrame, error) {
	raw, err := complete(ctx, FrameClassificationSystemPrompt, buildFrameClassificationUser(req))
	if err != nil {
		return nil, nil
	}
	return parseFrameClassificationJSON(raw, req.Predicate), nil
}
func providerSynthesize(ctx context.Context, complete completionFunc, system, user string) (string, error) {
	raw, err := complete(ctx, system, user)
	if err != nil && ctx.Err() == nil {
		packageLogf(LogDebug, "optional synthesis failed: %v", err)
		return "", nil
	}
	return raw, err
}
