package surriti

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const ExtractionSystemPrompt = "You are a knowledge-graph extractor. Your input has two clearly fenced \\\nsections:\n\n  CONTEXT (read-only, do NOT extract):\n    <prior episodes for reference -- use only to resolve pronouns and\n     to recognise entities; never re-emit a fact whose source text\n     lives only in this section>\n\n  CURRENT EPISODE (extract from this only):\n    <the new text -- every fact you emit must come from THIS section>\n\nIf the CONTEXT block is omitted there is no prior context to consider.\n\nReturn STRICT JSON with two arrays:\n\n{\"entities\":[{\"name\":\"...\",\"labels\":[\"...\"],\"summary\":\"...\"}],\n \"facts\":[{\"subject\":\"...\",\"predicate\":\"...\",\"object\":\"...\",\n           \"fact\":\"...\",\"operation\":\"assert\",\"temporal\":false,\n           \"singleton\":false,\"domain\":null,\"memory_class\":\"objective\",\n           \"relation_phrase\":\"...\",\"qualifiers\":{},\"argument_roles\":{},\n           \"source_span\":\"...\",\"replaces\":[]}]}\n\nWHAT COUNTS AS A FACT (extract these from CURRENT EPISODE):\n- Self-introductions and properties (\"my name is X\", \"I'm X\",\n  \"I am 5 months old\", \"I work at Acme\", \"I live in Berlin\",\n  \"I like pizza\", \"my birthday is October 14\").\n- Properties of other named entities (\"Alice works at Acme\").\n- Compound sentences: split into one fact per claim.\n- VALUES become entities: dates, ages, places, companies must\n  appear in `entities` AND be the `object` of the fact.\n  NEVER use placeholders like \"speaker\" or \"value\" as subject/object.\n- When in doubt, extract.\n\nWHAT TO SKIP (return no fact, but mention any named entities):\n- Pure interjections (\"hi\", \"thanks\", \"ok\", \"hmm\").\n- Pure questions (\"where do I work?\", \"what's my name?\").\n- Vague placeholder objects: if the object would be a meaningless\n  filler like \"world\", \"everywhere\", \"thing\", \"something\", \"nothing\",\n  \"someone\", DROP the fact entirely.\n\nFIELD REFERENCE (use defaults unless input suggests otherwise):\n  operation       : assert | terminate | correct | qualify | noop\n                    (default: assert)\n  temporal        : true/false — current state that can change\n  singleton       : true/false — only one value valid at a time\n  domain          : free-form bucket (employment, residence, etc.)\n  memory_class    : objective | preference | style | constraint |\n                    trait | sentiment (default: objective)\n  relation_phrase : verbatim verb phrase from source\n  qualifiers      : {condition: value} — scopes the claim\n  argument_roles  : {subject: role, object: role} — semantic roles\n  source_span     : verbatim text slice from CURRENT EPISODE\n  replaces        : [prior fact descriptions] — what this closes\n\nMEMORY_CLASS GUIDE:\n  objective   — verifiable claim about the world/user (\"Jessica\n                works at Target\", \"I am 32\", \"Acme is in Berlin\")\n  preference  — soft wish about how assistant/world should behave\n                (\"respond as X\", \"I prefer concise answers\")\n  style       — communication-style directive (\"be terse\", \"no\n                emojis\", \"write in bullet points\")\n  constraint  — hard rule / forbidden action (\"never call me after\n                9pm\", \"do not store credit card numbers\")\n  trait       — persistent personal trait/value/belief (\"values\n                privacy\", \"is risk-averse\", \"is a vegetarian\")\n  sentiment   — emotional pattern/opinion (\"dislikes small talk\",\n                \"loves jazz\")\n  Subjective directives (\"respond as\", \"prefer\", \"always\", \"never\",\n  \"I want you to\", \"stop doing\") are almost always preference/style/\n  constraint, NOT objective.\n\nWORKED EXAMPLES (predicate names are illustrative; real ones come\nfrom the input):\n\n  \"I work at Acme\" ->\n    {\"subject\":\"<speaker>\", \"predicate\":\"works_at\",\n     \"relation_phrase\":\"work at\", \"object\":\"Acme\",\n     \"fact\":\"... works at Acme.\",\n     \"operation\":\"assert\", \"temporal\":true, \"singleton\":true,\n     \"domain\":\"employment\", \"memory_class\":\"objective\",\n     \"argument_roles\":{\"subject\":\"employee\",\"object\":\"employer\"},\n     \"source_span\":\"I work at Acme\"}\n\n  \"I quit my job at Acme\" ->\n    {\"subject\":\"<speaker>\", \"predicate\":\"works_at\",\n     \"relation_phrase\":\"quit my job at\", \"object\":\"Acme\",\n     \"fact\":\"... quit working at Acme.\",\n     \"operation\":\"terminate\", \"temporal\":true, \"singleton\":true,\n     \"domain\":\"employment\", \"memory_class\":\"objective\",\n     \"source_span\":\"I quit my job at Acme\"}\n\n  \"I live in Florida during the winter\" ->\n    {\"subject\":\"<speaker>\", \"predicate\":\"lives_in\",\n     \"relation_phrase\":\"live in\", \"object\":\"Florida\",\n     \"fact\":\"... lives in Florida during the winter.\",\n     \"operation\":\"qualify\", \"temporal\":true, \"singleton\":true,\n     \"domain\":\"residence\", \"memory_class\":\"objective\",\n     \"qualifiers\":{\"season\":\"winter\"},\n     \"source_span\":\"I live in Florida during the winter\"}\n\n  \"Be terse and never use emojis\" ->\n    [{\"subject\":\"<speaker>\", \"predicate\":\"wants_assistant_style\",\n      \"relation_phrase\":\"be\", \"object\":\"terse\",\n      \"fact\":\"... wants the assistant to be terse.\",\n      \"operation\":\"assert\", \"temporal\":true, \"singleton\":false,\n      \"domain\":\"assistant_style\", \"memory_class\":\"style\",\n      \"source_span\":\"Be terse\"},\n     {\"subject\":\"<speaker>\", \"predicate\":\"forbids_assistant_action\",\n      \"relation_phrase\":\"never use\", \"object\":\"emojis\",\n      \"fact\":\"... forbids the assistant from using emojis.\",\n      \"operation\":\"assert\", \"temporal\":true, \"singleton\":false,\n      \"domain\":\"assistant_style\", \"memory_class\":\"constraint\",\n      \"source_span\":\"never use emojis\"}]\n\n  \"I sold the Civic and bought a Tesla\" ->\n    [{\"subject\":\"<speaker>\", \"predicate\":\"sold_vehicle\",\n      \"relation_phrase\":\"sold\", \"object\":\"Civic\",\n      \"fact\":\"... sold the Civic.\",\n      \"operation\":\"assert\", \"temporal\":false, \"singleton\":false,\n      \"domain\":\"vehicle\", \"memory_class\":\"objective\",\n      \"replaces\":[\"<speaker> drives Civic\",\"<speaker> owns Civic\"],\n      \"source_span\":\"I sold the Civic\"},\n     {\"subject\":\"<speaker>\", \"predicate\":\"drives\",\n      \"relation_phrase\":\"bought\", \"object\":\"Tesla\",\n      \"fact\":\"... drives a Tesla.\",\n      \"operation\":\"assert\", \"temporal\":true, \"singleton\":true,\n      \"domain\":\"vehicle\", \"memory_class\":\"objective\",\n      \"source_span\":\"bought a Tesla\"}]\n  Note: the *event* fact (sold/lost/replaced/disposed) carries a\n  `replaces` list naming the prior states it terminates, so the\n  engine can close them without a second contradiction-detection\n  round-trip.\n\nCOMPOUND CLAIMS — one sentence often packs multiple facts.\n\"I work night shifts at a hospital on Tuesdays and Thursdays\"\nyields THREE facts (works_at hospital; works_shift night;\nworks_on [Tuesdays, Thursdays]). \"I keep my passport in the blue\nsafe in the garage\" yields TWO facts (keeps_in passport->blue\nsafe; located_in blue safe->garage). Always decompose.\n\nSUBJECTIVE-DIRECTIVE PREDICATE VOCABULARY (use these exact\npredicates when the user tells the assistant how to behave):\n- `wants_assistant_persona`  - persona/role-play (\"respond as X\",\n                               \"act like X\")\n- `wants_assistant_style`    - positive style preferences\n                               (\"be terse\", \"use markdown\")\n- `forbids_assistant_action` - hard prohibitions\n                               (\"never X\", \"don't X\", \"stop doing X\")\n- `prefers_communication`    - communication preferences\n                               (\"text only\", \"no calls after 9pm\"\n                                -> use forbids_*)\n- `values`                   - trait-class assertions\n                               (\"I value X\", \"I care about X\")\n- `feels_about`              - sentiment-class assertions\n                               (\"I love jazz\", \"I dislike X\")\nFor all other facts use whatever snake_case predicate fits.\n\nHARD RULES (violations make the output unusable):\n- Extract facts ONLY from CURRENT EPISODE. CONTEXT is read-only.\n- NEVER invent entities, predicates, or relations not supported by\n  the input. Do NOT use placeholder names like Alice, Bob, Acme,\n  Foo, Bar unless they appear in the text.\n- NEVER emit a self-loop fact (subject == object). For naming, the\n  subject is the SPEAKER and the object is the new name.\n- Tokens that look like internal metadata — bracketed labels\n  (`[chat]`, `[turn-a]`), bare UUIDs — are NOT entities.\n- Use the EXACT entity name strings inside facts (subject/object).\n- Predicates are snake_case verbs. Avoid `related_to`.\n- Each fact's `fact` is a complete natural-language sentence.\n- Return ONLY the JSON object, no commentary, no markdown fences.\n"
const FrameClassificationSystemPrompt = "You classify a never-seen-before relation predicate into a generic\nframe so a temporal knowledge graph can reason over it without any\ndomain-specific code. Return STRICT JSON with these keys (no others,\nno markdown):\n\n  {\n    \"canonical_name\":   \"snake_case_verb_or_phrase\",\n    \"aliases\":          [\"other\", \"phrasings\"],\n    \"directionality\":   \"directed\" | \"symmetric\" | \"inverse_pair\" | \"unknown\",\n    \"temporal_kind\":    \"state\" | \"event\" | \"timeless\" | \"recurring\" | \"unknown\",\n    \"cardinality\":      \"one_current\" | \"many_current\" | \"many_historical\" | \"timeless\" | \"unknown\",\n    \"contradiction_policy\": \"replace\" | \"coexist\" | \"negate\" | \"uncertain\",\n    \"inverse_name\":     \"snake_case_inverse_or_null\",\n    \"subject_role\":     \"role_label_or_null\",\n    \"object_role\":      \"role_label_or_null\",\n    \"confidence\":       0.0 to 1.0\n  }\n\nGUIDANCE:\n- `directionality`: \"symmetric\" iff swapping subject and object is\n  semantically identical (\"sibling_of\", \"married_to\"). \"inverse_pair\"\n  iff there is a natural inverse predicate (\"parent_of\"/\"child_of\");\n  set `inverse_name` accordingly. Otherwise \"directed\".\n- `temporal_kind`: \"state\" for ongoing facts that can change over\n  time (residence, job); \"timeless\" for facts that never change\n  (birthplace, parentage); \"event\" for point-in-time happenings;\n  \"recurring\" for repeating activities.\n- `cardinality`: \"one_current\" iff at most one such fact can be\n  simultaneously true for a subject (current employer, current\n  residence). \"many_current\" if multiple coexist (friendships,\n  hobbies). \"many_historical\" for events that accumulate. \"timeless\"\n  for immutable facts.\n- `contradiction_policy`: \"replace\" iff a new value supersedes the\n  prior one (always pair with `one_current`). \"coexist\" for\n  many_current/timeless facts. \"negate\" when the predicate carries\n  explicit truth flips. \"uncertain\" when conflicting claims should\n  be flagged for human resolution rather than auto-merged.\n- `confidence` reflects how sure you are about the classification\n  itself, not the underlying fact.\n\nReturn JSON only.\n"
const ContradictionSystemPrompt = "You decide which prior facts are invalidated by a new fact. Return STRICT \\\nJSON: {\"invalidated_indexes\": [<int>, ...]}.\n\nA prior fact is invalidated when ALL of these are true:\n1. It has the SAME subject as the new fact, OR the new fact's subject\n   and object swap roles in a transfer event (e.g. \"Alice sold the\n   Civic\" invalidates prior facts where Alice's relationship TO the\n   Civic was active -- \"Alice drives the Civic\", \"Alice owns the\n   Civic\"). Object identity matters.\n2. The new fact materially supersedes it. The supersession can be\n   either:\n   a) SAME-PREDICATE replacement -- \"X works at A\" then \"X works at B\"\n      with one_current cardinality; \"X lives in P\" then \"X lives in Q\";\n      \"X is named Foo\" then \"X is renamed Bar\".\n   b) CROSS-PREDICATE state transition -- the new fact describes an\n      event that ENDS a prior state involving the same object:\n        * \"X sold/lost/discarded/gave away/totalled <object>\"\n          invalidates prior \"X drives/owns/uses/has/keeps <object>\".\n        * \"X moved <object> to <new place>\" invalidates prior\n          \"X keeps/stores <object> in <old place>\".\n        * \"X moved to <new place>\" invalidates prior\n          \"X lives_in <old place>\" (already covered by 2a if both\n          predicates canonicalize).\n        * \"Vet cleared <patient> of <condition>\" / \"<patient>\n          recovered from <condition>\" / \"<patient> is no longer\n          allergic to <substance>\" invalidates prior\n          \"<patient> is_allergic_to/has_condition <substance>\".\n        * \"<entity> closed/shut down/dissolved\" invalidates ongoing\n          relationships that depend on it being active.\n   c) EXPLICIT NEGATION -- \"no longer\", \"not anymore\", \"stopped\",\n      \"quit\" referencing the prior fact.\n3. The two facts cannot describe coexisting realities (different\n   qualifiers like seasons, scopes, etc.).\n\nUse object identity AGGRESSIVELY for transfer-of-state events: any\nprior fact whose object matches the new fact's object AND whose\npredicate describes an ongoing relationship that the event would\nnaturally end is invalidated.\n\nExamples that ARE contradictions (return their indexes):\n- new: \"Jordan sold the Honda Civic\", prior: \"Jordan drives the Honda\n  Civic\" -- selling ends driving.\n- new: \"Jordan moved the passport to the office desk drawer\", prior:\n  \"Jordan keeps the passport in the blue safe\" -- moving ends the old\n  storage.\n- new: \"The vet cleared Pixel of the chicken allergy\", prior: \"Pixel\n  is allergic to chicken\" -- clearance ends the allergy.\n- new: \"Ava moved to Seattle in March\", prior: \"Ava lives in Denver\"\n  -- residence change.\n\nExamples that are NOT contradictions (return `[]`):\n- new: \"Michael is_brother_of Mark\", prior: \"Michael works_with Mark\"\n  (family vs employment -- different facts about same pair).\n- new: \"Alice likes pizza\", prior: \"Alice lives_in Berlin\"\n  (different domains, no shared object).\n- new: \"Bob is_named Robert\", prior: \"Bob works_at Acme\"\n  (different domains).\n- new: \"Jordan bought a Tesla\", prior: \"Jordan drives a Civic\"\n  (different objects -- the Civic is unaffected by the Tesla\n  purchase; the Civic's status only changes if a separate\n  sold/disposed claim is made).\n\nWhen the new fact is itself an objective state (not an event) and\nshares no object with the prior, return `[]`.\n"

func buildExtractionUser(req ExtractionRequest) string {
	parts := []string{}
	if strings.TrimSpace(req.Context) != "" {
		parts = append(parts, "CONTEXT (read-only, do NOT extract; use only for pronoun/entity resolution):\n"+strings.TrimSpace(req.Context))
	}
	parts = append(parts, fmt.Sprintf("CURRENT EPISODE (group_id=%q; extract from this only):\n%s", req.GroupID, strings.TrimSpace(req.Content)))
	if len(req.EntityTypes) > 0 {
		keys := sortedEntityTypeKeys(req.EntityTypes)
		parts = append(parts, "Allowed entity types: "+strings.Join(keys, ", "))
	}
	if req.CustomInstructions != "" {
		parts = append(parts, "Additional instructions: "+req.CustomInstructions)
	}
	return strings.Join(parts, "\n\n")
}

func stripJSONFences(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "```") {
		if i := strings.IndexByte(text, '\n'); i >= 0 { text = text[i+1:] }
	}
	text = strings.TrimSpace(text)
	if strings.HasSuffix(text, "```") {
		text = strings.TrimSpace(strings.TrimSuffix(text, "```"))
	}
	return text
}

func stringsFromJSON(v any) []string {
	switch x := v.(type) {
	case nil:
		return []string{}
	case string:
		x = strings.TrimSpace(x); if x=="" { return []string{} }; return []string{x}
	case []any:
		out:=[]string{}
		for _,item:=range x { s:=strings.TrimSpace(fmt.Sprint(item)); if item!=nil&&s!="" { out=append(out,s) } }
		return out
	default:
		s:=strings.TrimSpace(fmt.Sprint(v)); if s=="" { return []string{} }; return []string{s}
	}
}

func parseExtractionJSON(raw string) (ExtractionResult,error) {
	var data map[string]any
	if err:=json.Unmarshal([]byte(stripJSONFences(raw)),&data);err!=nil {
		return ExtractionResult{},fmt.Errorf("%w: LLM did not return valid JSON: %.200q",ErrLLM,raw)
	}
	entities:=[]ExtractedEntity{}
	if list,ok:=data["entities"].([]any);ok {
		for _,item:=range list {
			m:=mapFromAny(item); if len(m)==0 { continue }
			names:=stringsFromJSON(m["name"])
			labels:=asStringSlice(m["labels"]);if len(labels)==0{labels=[]string{"Entity"}}
			for _,name:=range names { entities=append(entities,ExtractedEntity{Name:name,Summary:stringFromAny(m["summary"]),Labels:append([]string(nil),labels...)}) }
		}
	}
	facts:=[]ExtractedFact{}
	allowedClass:=map[string]struct{}{"objective":{},"preference":{},"style":{},"constraint":{},"trait":{},"sentiment":{}}
	if list,ok:=data["facts"].([]any);ok {
		for _,item:=range list {
			m:=mapFromAny(item);if len(m)==0{continue}
			subjects:=stringsFromJSON(m["subject"]);objects:=stringsFromJSON(m["object"]);if len(subjects)==0||len(objects)==0{continue}
			predicate:=strings.TrimSpace(stringFromAny(m["predicate"]));if predicate==""{predicate="related_to"}
			op:=FactOperation(strings.ToLower(strings.TrimSpace(stringFromAny(m["operation"]))));switch op{case FactAssert,FactTerminate,FactCorrect,FactQualify,FactNoop:default:op=FactAssert}
			mc:=strings.ToLower(strings.TrimSpace(stringFromAny(m["memory_class"])));if _,ok:=allowedClass[mc];!ok{mc="objective"}
			var domain *string;if d,ok:=m["domain"].(string);ok{d=strings.ToLower(strings.TrimSpace(d));if d!=""{domain=&d}}
			replaces:=stringsFromJSON(m["replaces"])
			conf:=1.0;if m["confidence"]!=nil{if v,ok:=toFloat(m["confidence"]);ok{conf=v}}
			var relationPhrase *string;if v,ok:=m["relation_phrase"].(string);ok&&strings.TrimSpace(v)!=""{v=strings.TrimSpace(v);relationPhrase=&v}
			qual:=mapFromAny(m["qualifiers"])
			roles:=map[string]string{};for k,v:=range mapFromAny(m["argument_roles"]){roles[k]=stringFromAny(v)}
			var sourceSpan *string;if v,ok:=m["source_span"].(string);ok&&strings.TrimSpace(v)!=""{v=strings.TrimSpace(v);sourceSpan=&v}
			var validAt,invalidAt *string
			if v:=stringFromAny(m["valid_at"]);v!=""{validAt=&v};if v:=stringFromAny(m["invalid_at"]);v!=""{invalidAt=&v}
			for _,sub:=range subjects{for _,obj:=range objects{
				factText:=strings.TrimSpace(stringFromAny(m["fact"]));if factText==""{factText=fmt.Sprintf("%s %s %s.",sub,predicate,obj)}
				facts=append(facts,ExtractedFact{Subject:sub,Predicate:predicate,Object:obj,Fact:factText,ValidAt:validAt,InvalidAt:invalidAt,Operation:op,Temporal:boolFromAny(m["temporal"]),Singleton:boolFromAny(m["singleton"]),Domain:domain,MemoryClass:mc,Replaces:append([]string(nil),replaces...),Confidence:conf,RelationPhrase:relationPhrase,Qualifiers:qual,ArgumentRoles:roles,SourceSpan:sourceSpan})
			}}
		}
	}
	return ExtractionResult{Entities:entities,Facts:facts},nil
}

func buildContradictionUser(req ContradictionRequest) string {
	parts:=[]string{}
	if req.NewFactStruct!=nil {
		f:=req.NewFactStruct
		domain:="<none>";if f.Domain!=nil{domain=*f.Domain}
		parts=append(parts,fmt.Sprintf("NEW FACT:\n  text:      %s\n  subject:   %s\n  predicate: %s\n  object:    %s\n  domain:    %s\n  operation: %s",req.NewFact,f.Subject,f.Predicate,f.Object,domain,f.Operation))
	}else{parts=append(parts,"NEW FACT: "+req.NewFact)}
	if len(req.Candidates)>0 {
		lines:=[]string{}
		for i,c:=range req.Candidates{domain:="<none>";if c.Domain!=nil{domain=*c.Domain};lines=append(lines,fmt.Sprintf("  %d.\n     text:      %s\n     subject:   %s\n     predicate: %s\n     object:    %s\n     domain:    %s",i,c.Fact,c.Subject,c.Predicate,c.Object,domain))}
		parts=append(parts,"PRIOR FACTS (indexed):\n"+strings.Join(lines,"\n"))
	}else{
		lines:=[]string{};for i,f:=range req.ExistingFacts{lines=append(lines,fmt.Sprintf("  %d. %s",i,f))}
		parts=append(parts,"PRIOR FACTS (indexed):\n"+strings.Join(lines,"\n"))
	}
	return strings.Join(parts,"\n\n")
}

func parseContradictionsJSON(raw string,n int)[]int{
	var data map[string]any;if json.Unmarshal([]byte(stripJSONFences(raw)),&data)!=nil{return []int{}}
	arr:=asAnySlice(data["invalidated_indexes"]);out:=[]int{}
	for _,x:=range arr{idx:=-1;switch v:=x.(type){case float64:idx=int(v);case int:idx=v;case json.Number:i,_:=strconv.Atoi(v.String());idx=i};if idx>=0&&idx<n{out=append(out,idx)}}
	return out
}

func buildFrameClassificationUser(req FrameClassificationRequest) string {
	lines:=[]string{"PREDICATE: "+req.Predicate}
	if req.SourceSpan!=""{lines=append(lines,"SOURCE SPAN: "+req.SourceSpan)}
	if req.SampleSubject!=""||req.SampleObject!=""{sub:=req.SampleSubject;if sub==""{sub="<subject>"};obj:=req.SampleObject;if obj==""{obj="<object>"};lines=append(lines,fmt.Sprintf("EXAMPLE TRIPLE: (%s) -[%s]-> (%s)",sub,req.Predicate,obj))}
	return strings.Join(lines,"\n")
}

func parseFrameClassificationJSON(raw,fallback string)*RelationFrame{
	var data map[string]any;if json.Unmarshal([]byte(stripJSONFences(raw)),&data)!=nil{return nil}
	canon:=strings.ToLower(strings.TrimSpace(stringFromAny(data["canonical_name"])));if canon==""{canon=strings.ToLower(strings.TrimSpace(fallback))};if canon==""{return nil}
	aliases:=[]string{};for _,a:=range stringsFromJSON(data["aliases"]){if a=strings.ToLower(strings.TrimSpace(a));a!=""{aliases=append(aliases,a)}}
	dir:=Directionality(strings.ToLower(stringFromAny(data["directionality"])));switch dir{case DirectionDirected,DirectionSymmetric,DirectionInversePair,DirectionUnknown:default:dir=DirectionUnknown}
	tk:=TemporalKind(strings.ToLower(stringFromAny(data["temporal_kind"])));switch tk{case TemporalState,TemporalEvent,TemporalTimeless,TemporalRecurring,TemporalUnknown:default:tk=TemporalUnknown}
	card:=Cardinality(strings.ToLower(stringFromAny(data["cardinality"])));switch card{case CardinalityOneCurrent,CardinalityManyCurrent,CardinalityManyHistorical,CardinalityTimeless,CardinalityUnknown:default:card=CardinalityUnknown}
	cp:=ContradictionPolicy(strings.ToLower(stringFromAny(data["contradiction_policy"])));switch cp{case ContradictionReplace,ContradictionCoexist,ContradictionNegate,ContradictionUncertain:default:cp=ContradictionUncertain}
	var inv,sr,or *string;if v:=strings.ToLower(strings.TrimSpace(stringFromAny(data["inverse_name"])));v!=""{inv=&v};if v:=strings.TrimSpace(stringFromAny(data["subject_role"]));v!=""{sr=&v};if v:=strings.TrimSpace(stringFromAny(data["object_role"]));v!=""{or=&v}
	conf:=.5;if data["confidence"]!=nil{if v,ok:=toFloat(data["confidence"]);ok{conf=v}};if conf<0{conf=0};if conf>1{conf=1}
	f:=RelationFrame{BaseModel:NewBaseModel(""),CanonicalName:canon,Aliases:aliases,Directionality:dir,TemporalKind:tk,Cardinality:card,ContradictionPolicy:cp,InverseName:inv,SubjectRole:sr,ObjectRole:or,Confidence:conf}
	return &f
}

type OpenAILLMClient struct {
	Model string
	APIKey string
	BaseURL string
	Temperature float64
	ExtraBody map[string]any
	HTTPClient *http.Client
}

func NewOpenAILLMClient(model,apiKey,baseURL string)(*OpenAILLMClient,error){
	if model==""{model="gpt-4o-mini"}
	if apiKey==""{apiKey=os.Getenv("OPENAI_API_KEY")}
	if apiKey==""{return nil,fmt.Errorf("%w: OPENAI_API_KEY is not set and no api key was provided",ErrConfig)}
	if baseURL==""{baseURL=os.Getenv("OPENAI_BASE_URL")};if baseURL==""{baseURL=os.Getenv("OPENAI_API_BASE")};if baseURL==""{baseURL="https://api.openai.com/v1"}
	return &OpenAILLMClient{Model:model,APIKey:apiKey,BaseURL:strings.TrimRight(baseURL,"/"),HTTPClient:&http.Client{Timeout:60*time.Second}},nil
}

func (c *OpenAILLMClient) complete(ctx context.Context,system,user string)(string,error){
	payload:=map[string]any{"model":c.Model,"temperature":c.Temperature,"response_format":map[string]any{"type":"json_object"},"messages":[]map[string]string{{"role":"system","content":system},{"role":"user","content":user}}}
	for k,v:=range c.ExtraBody{payload[k]=v}
	body,_:=json.Marshal(payload);req,err:=http.NewRequestWithContext(ctx,http.MethodPost,c.BaseURL+"/chat/completions",bytes.NewReader(body));if err!=nil{return "",err}
	req.Header.Set("Authorization","Bearer "+c.APIKey);req.Header.Set("Content-Type","application/json")
	client:=c.HTTPClient;if client==nil{client=http.DefaultClient}
	resp,err:=client.Do(req);if err!=nil{return "",fmt.Errorf("%w: OpenAI API call failed: %v",ErrLLM,err)};defer resp.Body.Close()
	raw,_:=io.ReadAll(io.LimitReader(resp.Body,8<<20));if resp.StatusCode<200||resp.StatusCode>=300{return "",fmt.Errorf("%w: OpenAI API call failed (%d): %s",ErrLLM,resp.StatusCode,strings.TrimSpace(string(raw)))}
	var parsed struct{Choices []struct{Message struct{Content string `json:"content"`} `json:"message"`} `json:"choices"`}
	if err:=json.Unmarshal(raw,&parsed);err!=nil||len(parsed.Choices)==0{return "",fmt.Errorf("%w: malformed OpenAI response",ErrLLM)}
	return strings.TrimSpace(parsed.Choices[0].Message.Content),nil
}
func(c *OpenAILLMClient)Extract(ctx context.Context,req ExtractionRequest)(ExtractionResult,error){raw,err:=c.complete(ctx,ExtractionSystemPrompt,buildExtractionUser(req));if err!=nil{return ExtractionResult{},err};return parseExtractionJSON(raw)}
func(c *OpenAILLMClient)FindContradictions(ctx context.Context,req ContradictionRequest)([]int,error){if len(req.ExistingFacts)==0{return []int{},nil};raw,err:=c.complete(ctx,ContradictionSystemPrompt,buildContradictionUser(req));if err!=nil{return nil,err};return parseContradictionsJSON(raw,len(req.ExistingFacts)),nil}
func(c *OpenAILLMClient)ClassifyRelationFrame(ctx context.Context,req FrameClassificationRequest)(*RelationFrame,error){raw,err:=c.complete(ctx,FrameClassificationSystemPrompt,buildFrameClassificationUser(req));if err!=nil{return nil,nil};return parseFrameClassificationJSON(raw,req.Predicate),nil}
func(c *OpenAILLMClient)Synthesize(ctx context.Context,system,user string)(string,error){return c.complete(ctx,system,user)}

type AnthropicLLMClient struct {Model,APIKey,BaseURL string;MaxTokens int;Temperature float64;HTTPClient *http.Client}
func NewAnthropicLLMClient(model,apiKey string)(*AnthropicLLMClient,error){if model==""{model="claude-3-5-haiku-latest"};if apiKey==""{apiKey=os.Getenv("ANTHROPIC_API_KEY")};if apiKey==""{return nil,fmt.Errorf("%w: ANTHROPIC_API_KEY is not set and no api key was provided",ErrConfig)};return &AnthropicLLMClient{Model:model,APIKey:apiKey,BaseURL:"https://api.anthropic.com/v1",MaxTokens:2048,HTTPClient:&http.Client{Timeout:60*time.Second}},nil}
func supportsAnthropicSampling(model string)bool{m:=strings.ToLower(strings.TrimSpace(model));for _,p:=range []string{"claude-fable-","claude-mythos-","claude-opus-5","claude-opus-4-7","claude-opus-4-8","claude-sonnet-5"}{if strings.HasPrefix(m,p){return false}};return true}
func(c *AnthropicLLMClient)complete(ctx context.Context,system,user string)(string,error){payload:=map[string]any{"model":c.Model,"max_tokens":c.MaxTokens,"system":system,"messages":[]map[string]string{{"role":"user","content":user}}};if supportsAnthropicSampling(c.Model){payload["temperature"]=c.Temperature};body,_:=json.Marshal(payload);req,err:=http.NewRequestWithContext(ctx,http.MethodPost,strings.TrimRight(c.BaseURL,"/")+"/messages",bytes.NewReader(body));if err!=nil{return "",err};req.Header.Set("x-api-key",c.APIKey);req.Header.Set("anthropic-version","2023-06-01");req.Header.Set("content-type","application/json");client:=c.HTTPClient;if client==nil{client=http.DefaultClient};resp,err:=client.Do(req);if err!=nil{return "",fmt.Errorf("%w: Anthropic API call failed: %v",ErrLLM,err)};defer resp.Body.Close();raw,_:=io.ReadAll(io.LimitReader(resp.Body,8<<20));if resp.StatusCode<200||resp.StatusCode>=300{return "",fmt.Errorf("%w: Anthropic API call failed (%d): %s",ErrLLM,resp.StatusCode,strings.TrimSpace(string(raw)))};var parsed struct{Content []struct{Text string `json:"text"`} `json:"content"`};if json.Unmarshal(raw,&parsed)!=nil{return "",fmt.Errorf("%w: malformed Anthropic response",ErrLLM)};var b strings.Builder;for _,x:=range parsed.Content{b.WriteString(x.Text)};return strings.TrimSpace(b.String()),nil}
func(c *AnthropicLLMClient)Extract(ctx context.Context,req ExtractionRequest)(ExtractionResult,error){raw,err:=c.complete(ctx,ExtractionSystemPrompt,buildExtractionUser(req));if err!=nil{return ExtractionResult{},err};return parseExtractionJSON(raw)}
func(c *AnthropicLLMClient)FindContradictions(ctx context.Context,req ContradictionRequest)([]int,error){if len(req.ExistingFacts)==0{return []int{},nil};raw,err:=c.complete(ctx,ContradictionSystemPrompt,buildContradictionUser(req));if err!=nil{return nil,err};return parseContradictionsJSON(raw,len(req.ExistingFacts)),nil}
func(c *AnthropicLLMClient)ClassifyRelationFrame(ctx context.Context,req FrameClassificationRequest)(*RelationFrame,error){raw,err:=c.complete(ctx,FrameClassificationSystemPrompt,buildFrameClassificationUser(req));if err!=nil{return nil,nil};return parseFrameClassificationJSON(raw,req.Predicate),nil}
func(c *AnthropicLLMClient)Synthesize(ctx context.Context,system,user string)(string,error){return c.complete(ctx,system,user)}
