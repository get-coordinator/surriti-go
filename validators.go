package surriti

import "strings"

var IdentityPredicates = map[string]struct{}{
	"is_named": {}, "is_called": {}, "is_self": {}, "is_aka": {}, "MENTIONS_WITH": {}, "mentions_with": {},
}
var locationFillers = map[string]struct{}{"world": {}, "earth": {}, "universe": {}, "everywhere": {}, "anywhere": {}, "somewhere": {}}
var locationPredicates = map[string]struct{}{"lives_in": {}, "located_in": {}, "from": {}, "resides_in": {}, "based_in": {}}

func RepairFact(f ExtractedFact, speakerID, speakerName string) *ExtractedFact {
	_ = speakerName
	f.Subject = strings.TrimSpace(f.Subject)
	f.Object = strings.TrimSpace(f.Object)
	f.Predicate = strings.ToLower(strings.TrimSpace(f.Predicate))
	if f.Subject == "" || f.Object == "" || f.Predicate == "" {
		return nil
	}
	if _, ok := locationPredicates[f.Predicate]; ok {
		if _, bad := locationFillers[strings.ToLower(f.Object)]; bad {
			return nil
		}
	}
	if f.Subject == f.Object {
		if _, ok := IdentityPredicates[f.Predicate]; ok {
			if speakerID != "" && f.Subject != speakerID && speakerID != f.Object {
				f.Subject = speakerID
			}
			return &f
		}
		return nil
	}
	if f.Operation == FactTerminate && f.Object == "" {
		return nil
	}
	return &f
}
