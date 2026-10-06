package surriti

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Directionality string
type TemporalKind string
type Cardinality string
type ContradictionPolicy string

const (
	DirectionDirected    Directionality = "directed"
	DirectionSymmetric   Directionality = "symmetric"
	DirectionInversePair Directionality = "inverse_pair"
	DirectionUnknown     Directionality = "unknown"

	TemporalState     TemporalKind = "state"
	TemporalEvent     TemporalKind = "event"
	TemporalTimeless  TemporalKind = "timeless"
	TemporalRecurring TemporalKind = "recurring"
	TemporalUnknown   TemporalKind = "unknown"

	CardinalityOneCurrent     Cardinality = "one_current"
	CardinalityManyCurrent    Cardinality = "many_current"
	CardinalityManyHistorical Cardinality = "many_historical"
	CardinalityTimeless       Cardinality = "timeless"
	CardinalityUnknown        Cardinality = "unknown"

	ContradictionReplace   ContradictionPolicy = "replace"
	ContradictionCoexist   ContradictionPolicy = "coexist"
	ContradictionNegate    ContradictionPolicy = "negate"
	ContradictionUncertain ContradictionPolicy = "uncertain"
)

type RelationFrame struct {
	BaseModel
	CanonicalName       string              `json:"canonical_name"`
	Aliases             []string            `json:"aliases"`
	Description         string              `json:"description"`
	Directionality      Directionality      `json:"directionality"`
	TemporalKind        TemporalKind        `json:"temporal_kind"`
	Cardinality         Cardinality         `json:"cardinality"`
	ContradictionPolicy ContradictionPolicy `json:"contradiction_policy"`
	InverseName         *string             `json:"inverse_name,omitempty"`
	SubjectRole         *string             `json:"subject_role,omitempty"`
	ObjectRole          *string             `json:"object_role,omitempty"`
	Confidence          float64             `json:"confidence"`
}

func (f RelationFrame) Matches(predicate string) bool {
	p := lowerTrim(predicate)
	if p == "" {
		return false
	}
	if p == strings.ToLower(f.CanonicalName) {
		return true
	}
	for _, a := range f.Aliases {
		if p == strings.ToLower(a) {
			return true
		}
	}
	return false
}

func QualifierHash(qualifiers map[string]any) string {
	if len(qualifiers) == 0 {
		return ""
	}
	payload, err := pythonCanonicalJSON(qualifiers)
	if err != nil {
		return ""
	}
	return blake2b64Hex([]byte(payload))
}

// pythonCanonicalJSON reproduces json.dumps(v, sort_keys=True,
// separators=(",", ":"), default=str). Exact cross-language output matters
// because the bytes are part of persistent slot/fact identity.
func pythonCanonicalJSON(v any) (string, error) {
	if v == nil { return "null", nil }
	switch x := v.(type) {
	case string:
		return pythonJSONString(x), nil
	case bool:
		if x { return "true", nil }
		return "false", nil
	case int:
		return strconv.FormatInt(int64(x), 10), nil
	case int8:
		return strconv.FormatInt(int64(x), 10), nil
	case int16:
		return strconv.FormatInt(int64(x), 10), nil
	case int32:
		return strconv.FormatInt(int64(x), 10), nil
	case int64:
		return strconv.FormatInt(x, 10), nil
	case uint:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint8:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint16:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint32:
		return strconv.FormatUint(uint64(x), 10), nil
	case uint64:
		return strconv.FormatUint(x, 10), nil
	case float32:
		return pythonFloat(float64(x)), nil
	case float64:
		return pythonFloat(x), nil
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			f, err := x.Float64()
			if err != nil { return "", err }
			return pythonFloat(f), nil
		}
		return string(x), nil
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x { keys = append(keys, k) }
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			value, err := pythonCanonicalJSON(x[k])
			if err != nil { return "", err }
			parts = append(parts, pythonJSONString(k)+":"+value)
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	}

	rv := reflect.ValueOf(v)
	if !rv.IsValid() { return "null", nil }
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() { return "null", nil }
		return pythonCanonicalJSON(rv.Elem().Interface())
	}
	if rv.Kind() == reflect.Map && rv.Type().Key().Kind() == reflect.String {
		keys := rv.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
		parts := make([]string, 0, len(keys))
		for _, key := range keys {
			value, err := pythonCanonicalJSON(rv.MapIndex(key).Interface())
			if err != nil { return "", err }
			parts = append(parts, pythonJSONString(key.String())+":"+value)
		}
		return "{" + strings.Join(parts, ",") + "}", nil
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		parts := make([]string, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			value, err := pythonCanonicalJSON(rv.Index(i).Interface())
			if err != nil { return "", err }
			parts[i] = value
		}
		return "[" + strings.Join(parts, ",") + "]", nil
	}
	return pythonJSONString(fmt.Sprint(v)), nil
}

func pythonFloat(v float64) string {
	if math.IsNaN(v) { return "NaN" }
	if math.IsInf(v, 1) { return "Infinity" }
	if math.IsInf(v, -1) { return "-Infinity" }
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") { s += ".0" }
	return s
}

func pythonJSONString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else if r <= 0x7f {
				b.WriteRune(r)
			} else if r <= 0xffff {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				r -= 0x10000
				hi := 0xd800 + (r >> 10)
				lo := 0xdc00 + (r & 0x3ff)
				fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func MakeSlotKey(groupID, subjectUUID, canonicalName string, qualifiers map[string]any) string {
	return strings.Join([]string{trim(groupID), trim(subjectUUID), lowerTrim(canonicalName), QualifierHash(qualifiers)}, "::")
}

func NormalizeSymmetric(subjectUUID, objectUUID string) (string, string) {
	if subjectUUID == "" || objectUUID == "" || subjectUUID <= objectUUID {
		return subjectUUID, objectUUID
	}
	return objectUUID, subjectUUID
}

func strptr(v string) *string { return &v }

func ensureRelationFrameIdentity(f RelationFrame, groupID string) RelationFrame {
	if f.UUID == "" {
		f.BaseModel = NewBaseModel(groupID)
	} else if f.CreatedAt.IsZero() {
		f.CreatedAt = utcNow()
	}
	return f
}

var defaultFramesIdentityOnce sync.Once

var DefaultFrames = []RelationFrame{
	{CanonicalName: "spouse_of", Aliases: []string{"wife_of", "husband_of", "married_to", "partner_of"}, Directionality: DirectionSymmetric, TemporalKind: TemporalState, Cardinality: CardinalityOneCurrent, ContradictionPolicy: ContradictionReplace, Confidence: .9},
	{CanonicalName: "parent_of", Aliases: []string{"father_of", "mother_of", "mom_of", "dad_of"}, Directionality: DirectionInversePair, InverseName: strptr("child_of"), TemporalKind: TemporalTimeless, Cardinality: CardinalityManyCurrent, ContradictionPolicy: ContradictionCoexist, Confidence: .9},
	{CanonicalName: "child_of", Aliases: []string{"son_of", "daughter_of"}, Directionality: DirectionInversePair, InverseName: strptr("parent_of"), TemporalKind: TemporalTimeless, Cardinality: CardinalityManyCurrent, ContradictionPolicy: ContradictionCoexist, Confidence: .9},
	{CanonicalName: "sibling_of", Aliases: []string{"brother_of", "sister_of"}, Directionality: DirectionSymmetric, TemporalKind: TemporalTimeless, Cardinality: CardinalityManyCurrent, ContradictionPolicy: ContradictionCoexist, Confidence: .9},
	{CanonicalName: "friend_of", Aliases: []string{"friends_with"}, Directionality: DirectionSymmetric, TemporalKind: TemporalState, Cardinality: CardinalityManyCurrent, ContradictionPolicy: ContradictionCoexist, Confidence: .7},
	{CanonicalName: "lives_in", Aliases: []string{"resides_in", "based_in", "located_in", "from"}, Directionality: DirectionDirected, TemporalKind: TemporalState, Cardinality: CardinalityOneCurrent, ContradictionPolicy: ContradictionReplace, Confidence: .85},
	{CanonicalName: "works_at", Aliases: []string{"employed_by", "employee_of", "works_for"}, Directionality: DirectionDirected, TemporalKind: TemporalState, Cardinality: CardinalityOneCurrent, ContradictionPolicy: ContradictionReplace, Confidence: .85},
	{CanonicalName: "member_of", Aliases: []string{"belongs_to", "part_of"}, Directionality: DirectionDirected, TemporalKind: TemporalState, Cardinality: CardinalityManyCurrent, ContradictionPolicy: ContradictionCoexist, Confidence: .7},
	{CanonicalName: "owns", Aliases: []string{"has", "possesses"}, Directionality: DirectionDirected, TemporalKind: TemporalState, Cardinality: CardinalityManyCurrent, ContradictionPolicy: ContradictionCoexist, Confidence: .7},
	{CanonicalName: "owns_pet", Aliases: []string{"has_pet", "owns_dog", "owns_cat", "pet_is"}, Directionality: DirectionDirected, TemporalKind: TemporalState, Cardinality: CardinalityManyCurrent, ContradictionPolicy: ContradictionCoexist, Confidence: .8},
	{CanonicalName: "is_named", Aliases: []string{"is_called", "is_aka", "name_is", "goes_by"}, Directionality: DirectionDirected, SubjectRole: strptr("self"), TemporalKind: TemporalState, Cardinality: CardinalityManyCurrent, ContradictionPolicy: ContradictionCoexist, Confidence: .95},
	{CanonicalName: "born_in", Aliases: []string{"was_born_in", "birthplace_is"}, Directionality: DirectionDirected, TemporalKind: TemporalTimeless, Cardinality: CardinalityOneCurrent, ContradictionPolicy: ContradictionReplace, Confidence: .95},
	{CanonicalName: "located_in", Aliases: []string{"situated_in", "found_in"}, Directionality: DirectionDirected, TemporalKind: TemporalState, Cardinality: CardinalityOneCurrent, ContradictionPolicy: ContradictionReplace, Confidence: .7},
	{CanonicalName: "contains", Aliases: []string{"includes", "holds"}, Directionality: DirectionInversePair, InverseName: strptr("part_of"), TemporalKind: TemporalState, Cardinality: CardinalityManyCurrent, ContradictionPolicy: ContradictionCoexist, Confidence: .7},
	{CanonicalName: "precedes", Aliases: []string{"before", "happens_before"}, Directionality: DirectionInversePair, InverseName: strptr("follows"), TemporalKind: TemporalEvent, Cardinality: CardinalityManyHistorical, ContradictionPolicy: ContradictionCoexist, Confidence: .7},
}

type RelationFrameRegistry struct {
	mu         sync.RWMutex
	global     map[string]RelationFrame
	byGroup    map[string]map[string]RelationFrame
	classifier RelationFrameClassifier
}

func NewRelationFrameRegistry(seedDefaults bool, classifier RelationFrameClassifier) *RelationFrameRegistry {
	r := &RelationFrameRegistry{global: map[string]RelationFrame{}, byGroup: map[string]map[string]RelationFrame{}, classifier: classifier}
	if seedDefaults {
		defaultFramesIdentityOnce.Do(func() {
			for i, f := range DefaultFrames {
				DefaultFrames[i] = ensureRelationFrameIdentity(f, "")
			}
		})
		for _, f := range DefaultFrames {
			r.registerGlobalLocked(cloneRelationFrame(f))
		}
	}
	return r
}

func frameAliasKeys(f RelationFrame) []string {
	keys := []string{lowerTrim(f.CanonicalName)}
	for _, a := range f.Aliases {
		if k := lowerTrim(a); k != "" {
			keys = append(keys, k)
		}
	}
	return keys
}

func cloneRelationFrame(f RelationFrame) RelationFrame {
	f.Aliases = append([]string(nil), f.Aliases...)
	return f
}

func (r *RelationFrameRegistry) registerGlobalLocked(f RelationFrame) {
	if f.UUID == "" {
		f.UUID = newUUID()
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = utcNow()
	}
	f.GroupID = ""
	for _, k := range frameAliasKeys(f) {
		if k != "" {
			r.global[k] = cloneRelationFrame(f)
		}
	}
}

func (r *RelationFrameRegistry) Register(f RelationFrame, groupID string) RelationFrame {
	f = ensureRelationFrameIdentity(cloneRelationFrame(f), groupID)
	if f.UUID == "" {
		f.UUID = newUUID()
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = utcNow()
	}
	f.GroupID = groupID
	r.mu.Lock()
	defer r.mu.Unlock()
	if groupID == "" {
		r.registerGlobalLocked(f)
		return cloneRelationFrame(f)
	}
	b := r.byGroup[groupID]
	if b == nil {
		b = map[string]RelationFrame{}
		r.byGroup[groupID] = b
	}
	for _, k := range frameAliasKeys(f) {
		if k != "" {
			b[k] = cloneRelationFrame(f)
		}
	}
	return cloneRelationFrame(f)
}

func (r *RelationFrameRegistry) Get(predicate, groupID string) (RelationFrame, bool) {
	key := lowerTrim(predicate)
	if key == "" {
		return RelationFrame{}, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if groupID != "" {
		if b := r.byGroup[groupID]; b != nil {
			if f, ok := b[key]; ok {
				return cloneRelationFrame(f), true
			}
		}
	}
	f, ok := r.global[key]
	return cloneRelationFrame(f), ok
}

func (r *RelationFrameRegistry) Resolve(ctx context.Context, req FrameClassificationRequest, groupID string) (RelationFrame, bool) {
	if f, ok := r.Get(req.Predicate, groupID); ok {
		return f, true
	}
	if r.classifier == nil {
		return RelationFrame{}, false
	}
	f, err := r.classifier.ClassifyRelationFrame(ctx, req)
	if err != nil || f == nil {
		return RelationFrame{}, false
	}
	alias := lowerTrim(req.Predicate)
	if alias != "" && !f.Matches(alias) {
		f.Aliases = append(append([]string{}, f.Aliases...), alias)
	}
	return r.Register(*f, groupID), true
}

func (r *RelationFrameRegistry) AllFrames(groupID string) []RelationFrame {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := map[string]RelationFrame{}
	for _, f := range r.global {
		seen[f.CanonicalName] = cloneRelationFrame(f)
	}
	if groupID != "" {
		for _, f := range r.byGroup[groupID] {
			seen[f.CanonicalName] = cloneRelationFrame(f)
		}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]RelationFrame, 0, len(keys))
	for _, k := range keys {
		out = append(out, seen[k])
	}
	return out
}
