package surriti

// Prompt contracts are shared with Python and pinned by golden tests.
const ExtractionSystemPrompt = `You are a knowledge-graph extractor. Your input has two clearly fenced sections:

  CONTEXT (read-only, do NOT extract):
    <prior episodes for reference -- use only to resolve pronouns and
     to recognise entities; never re-emit a fact whose source text
     lives only in this section>

  CURRENT EPISODE (extract from this only):
    <the new text -- every fact you emit must come from THIS section>

If the CONTEXT block is omitted there is no prior context to consider.

Return STRICT JSON with two arrays:

{"entities":[{"name":"...","labels":["..."],"summary":"..."}],
 "facts":[{"subject":"...","predicate":"...","object":"...",
           "fact":"...","operation":"assert","temporal":false,
           "singleton":false,"domain":null,"memory_class":"objective",
           "valid_at":null,"invalid_at":null,
           "relation_phrase":"...","qualifiers":{},"argument_roles":{},
           "source_span":"...","replaces":[]}]}

WHAT COUNTS AS A FACT (extract these from CURRENT EPISODE):
- Self-introductions and properties ("my name is X", "I'm X",
  "I am 5 months old", "I work at Acme", "I live in Berlin",
  "I like pizza", "my birthday is October 14").
- Properties of other named entities ("Alice works at Acme").
- Compound sentences: split into one fact per claim.
- VALUES become entities: dates, ages, places, companies must
  appear in ` + "`" + `entities` + "`" + ` AND be the ` + "`" + `object` + "`" + ` of the fact.
  NEVER use placeholders like "speaker" or "value" as subject/object.
- When in doubt, extract.

WHAT TO SKIP (return no fact, but mention any named entities):
- Pure interjections ("hi", "thanks", "ok", "hmm").
- Pure questions ("where do I work?", "what's my name?").
- Vague placeholder objects: if the object would be a meaningless
  filler like "world", "everywhere", "thing", "something", "nothing",
  "someone", DROP the fact entirely.

FIELD REFERENCE (use defaults unless input suggests otherwise):
  operation       : assert | terminate | correct | qualify | noop
                    (default: assert)
  valid_at        : ISO 8601 UTC timestamp for an explicit start date; otherwise null
  invalid_at      : ISO 8601 UTC timestamp for an explicit end date; otherwise null
                    Dates defining validity belong here, not in qualifiers.
  temporal        : true/false — current state that can change
  singleton       : true/false — only one value valid at a time
  domain          : free-form bucket (employment, residence, etc.)
  memory_class    : objective | preference | style | constraint |
                    trait | sentiment (default: objective)
  relation_phrase : verbatim verb phrase from source
  qualifiers      : {condition: value} — scopes the claim
  argument_roles  : {subject: role, object: role} — semantic roles
  source_span     : verbatim text slice from CURRENT EPISODE
  replaces        : [prior fact descriptions] — what this closes

MEMORY_CLASS GUIDE:
  objective   — verifiable claim about the world/user ("Jessica
                works at Target", "I am 32", "Acme is in Berlin")
  preference  — soft wish about how assistant/world should behave
                ("respond as X", "I prefer concise answers")
  style       — communication-style directive ("be terse", "no
                emojis", "write in bullet points")
  constraint  — hard rule / forbidden action ("never call me after
                9pm", "do not store credit card numbers")
  trait       — persistent personal trait/value/belief ("values
                privacy", "is risk-averse", "is a vegetarian")
  sentiment   — emotional pattern/opinion ("dislikes small talk",
                "loves jazz")
  Subjective directives ("respond as", "prefer", "always", "never",
  "I want you to", "stop doing") are almost always preference/style/
  constraint, NOT objective.

WORKED EXAMPLES (predicate names are illustrative; real ones come
from the input):

  "I work at Acme" ->
    {"subject":"<speaker>", "predicate":"works_at",
     "relation_phrase":"work at", "object":"Acme",
     "fact":"... works at Acme.",
     "operation":"assert", "temporal":true, "singleton":true,
     "domain":"employment", "memory_class":"objective",
     "argument_roles":{"subject":"employee","object":"employer"},
     "source_span":"I work at Acme"}

  "Judy started working at Acme on January 1, 2019" ->
    {"subject":"Judy", "predicate":"works_at", "object":"Acme",
     "fact":"Judy started working at Acme on January 1, 2019.",
     "operation":"assert", "temporal":true, "singleton":true,
     "valid_at":"2019-01-01T00:00:00Z", "invalid_at":null,
     "qualifiers":{}, "domain":"employment"}

  "Robert Smith goes by Bob" ->
    {"subject":"Robert Smith", "predicate":"is_named", "object":"Bob",
     "fact":"Robert Smith goes by Bob.", "operation":"assert"}

  "I quit my job at Acme" ->
    {"subject":"<speaker>", "predicate":"works_at",
     "relation_phrase":"quit my job at", "object":"Acme",
     "fact":"... quit working at Acme.",
     "operation":"terminate", "temporal":true, "singleton":true,
     "domain":"employment", "memory_class":"objective",
     "source_span":"I quit my job at Acme"}

  "I live in Florida during the winter" ->
    {"subject":"<speaker>", "predicate":"lives_in",
     "relation_phrase":"live in", "object":"Florida",
     "fact":"... lives in Florida during the winter.",
     "operation":"qualify", "temporal":true, "singleton":true,
     "domain":"residence", "memory_class":"objective",
     "qualifiers":{"season":"winter"},
     "source_span":"I live in Florida during the winter"}

  "Be terse and never use emojis" ->
    [{"subject":"<speaker>", "predicate":"wants_assistant_style",
      "relation_phrase":"be", "object":"terse",
      "fact":"... wants the assistant to be terse.",
      "operation":"assert", "temporal":true, "singleton":false,
      "domain":"assistant_style", "memory_class":"style",
      "source_span":"Be terse"},
     {"subject":"<speaker>", "predicate":"forbids_assistant_action",
      "relation_phrase":"never use", "object":"emojis",
      "fact":"... forbids the assistant from using emojis.",
      "operation":"assert", "temporal":true, "singleton":false,
      "domain":"assistant_style", "memory_class":"constraint",
      "source_span":"never use emojis"}]

  "I sold the Civic and bought a Tesla" ->
    [{"subject":"<speaker>", "predicate":"sold_vehicle",
      "relation_phrase":"sold", "object":"Civic",
      "fact":"... sold the Civic.",
      "operation":"assert", "temporal":false, "singleton":false,
      "domain":"vehicle", "memory_class":"objective",
      "replaces":["<speaker> drives Civic","<speaker> owns Civic"],
      "source_span":"I sold the Civic"},
     {"subject":"<speaker>", "predicate":"drives",
      "relation_phrase":"bought", "object":"Tesla",
      "fact":"... drives a Tesla.",
      "operation":"assert", "temporal":true, "singleton":true,
      "domain":"vehicle", "memory_class":"objective",
      "source_span":"bought a Tesla"}]
  Note: the *event* fact (sold/lost/replaced/disposed) carries a
  ` + "`" + `replaces` + "`" + ` list naming the prior states it terminates, so the
  engine can close them without a second contradiction-detection
  round-trip.

COMPOUND CLAIMS — one sentence often packs multiple facts.
"I work night shifts at a hospital on Tuesdays and Thursdays"
yields THREE facts (works_at hospital; works_shift night;
works_on [Tuesdays, Thursdays]). "I keep my passport in the blue
safe in the garage" yields TWO facts (keeps_in passport->blue
safe; located_in blue safe->garage). Always decompose.

SUBJECTIVE-DIRECTIVE PREDICATE VOCABULARY (use these exact
predicates when the user tells the assistant how to behave):
- ` + "`" + `wants_assistant_persona` + "`" + `  - persona/role-play ("respond as X",
                               "act like X")
- ` + "`" + `wants_assistant_style` + "`" + `    - positive style preferences
                               ("be terse", "use markdown")
- ` + "`" + `forbids_assistant_action` + "`" + ` - hard prohibitions
                               ("never X", "don't X", "stop doing X")
- ` + "`" + `prefers_communication` + "`" + `    - communication preferences
                               ("text only", "no calls after 9pm"
                                -> use forbids_*)
- ` + "`" + `values` + "`" + `                   - trait-class assertions
                               ("I value X", "I care about X")
- ` + "`" + `feels_about` + "`" + `              - sentiment-class assertions
                               ("I love jazz", "I dislike X")
For all other facts use whatever snake_case predicate fits.

HARD RULES (violations make the output unusable):
- Extract facts ONLY from CURRENT EPISODE. CONTEXT is read-only.
- NEVER invent entities, predicates, or relations not supported by
  the input. Do NOT use placeholder names like Alice, Bob, Acme,
  Foo, Bar unless they appear in the text.
- NEVER emit a self-loop fact (subject == object). For naming, the
  subject is the SPEAKER and the object is the new name.
- Tokens that look like internal metadata — bracketed labels
  (` + "`" + `[chat]` + "`" + `, ` + "`" + `[turn-a]` + "`" + `), bare UUIDs — are NOT entities.
- Use the EXACT entity name strings inside facts (subject/object).
- Predicates are snake_case verbs. Avoid ` + "`" + `related_to` + "`" + `.
- Each fact's ` + "`" + `fact` + "`" + ` is a complete natural-language sentence.
- Return ONLY the JSON object, no commentary, no markdown fences.
`

const FrameClassificationSystemPrompt = `You classify a never-seen-before relation predicate into a generic
frame so a temporal knowledge graph can reason over it without any
domain-specific code. Return STRICT JSON with these keys (no others,
no markdown):

  {
    "canonical_name":   "snake_case_verb_or_phrase",
    "aliases":          ["other", "phrasings"],
    "directionality":   "directed" | "symmetric" | "inverse_pair" | "unknown",
    "temporal_kind":    "state" | "event" | "timeless" | "recurring" | "unknown",
    "cardinality":      "one_current" | "many_current" | "many_historical" | "timeless" | "unknown",
    "contradiction_policy": "replace" | "coexist" | "negate" | "uncertain",
    "inverse_name":     "snake_case_inverse_or_null",
    "subject_role":     "role_label_or_null",
    "object_role":      "role_label_or_null",
    "confidence":       0.0 to 1.0
  }

GUIDANCE:
- ` + "`" + `directionality` + "`" + `: "symmetric" iff swapping subject and object is
  semantically identical ("sibling_of", "married_to"). "inverse_pair"
  iff there is a natural inverse predicate ("parent_of"/"child_of");
  set ` + "`" + `inverse_name` + "`" + ` accordingly. Otherwise "directed".
- ` + "`" + `temporal_kind` + "`" + `: "state" for ongoing facts that can change over
  time (residence, job); "timeless" for facts that never change
  (birthplace, parentage); "event" for point-in-time happenings;
  "recurring" for repeating activities.
- ` + "`" + `cardinality` + "`" + `: "one_current" iff at most one such fact can be
  simultaneously true for a subject (current employer, current
  residence). "many_current" if multiple coexist (friendships,
  hobbies). "many_historical" for events that accumulate. "timeless"
  for immutable facts.
- ` + "`" + `contradiction_policy` + "`" + `: "replace" iff a new value supersedes the
  prior one (always pair with ` + "`" + `one_current` + "`" + `). "coexist" for
  many_current/timeless facts. "negate" when the predicate carries
  explicit truth flips. "uncertain" when conflicting claims should
  be flagged for human resolution rather than auto-merged.
- ` + "`" + `confidence` + "`" + ` reflects how sure you are about the classification
  itself, not the underlying fact.

Return JSON only.
`

const ContradictionSystemPrompt = `You decide which prior facts are invalidated by a new fact. Return STRICT JSON: {"invalidated_indexes": [<int>, ...]}.

A prior fact is invalidated when ALL of these are true:
1. It has the SAME subject as the new fact, OR the new fact's subject
   and object swap roles in a transfer event (e.g. "Alice sold the
   Civic" invalidates prior facts where Alice's relationship TO the
   Civic was active -- "Alice drives the Civic", "Alice owns the
   Civic"). Object identity matters.
2. The new fact materially supersedes it. The supersession can be
   either:
   a) SAME-PREDICATE replacement -- "X works at A" then "X works at B"
      with one_current cardinality; "X lives in P" then "X lives in Q";
      "X is named Foo" then "X is renamed Bar".
   b) CROSS-PREDICATE state transition -- the new fact describes an
      event that ENDS a prior state involving the same object:
        * "X sold/lost/discarded/gave away/totalled <object>"
          invalidates prior "X drives/owns/uses/has/keeps <object>".
        * "X moved <object> to <new place>" invalidates prior
          "X keeps/stores <object> in <old place>".
        * "X moved to <new place>" invalidates prior
          "X lives_in <old place>" (already covered by 2a if both
          predicates canonicalize).
        * "Vet cleared <patient> of <condition>" / "<patient>
          recovered from <condition>" / "<patient> is no longer
          allergic to <substance>" invalidates prior
          "<patient> is_allergic_to/has_condition <substance>".
        * "<entity> closed/shut down/dissolved" invalidates ongoing
          relationships that depend on it being active.
   c) EXPLICIT NEGATION -- "no longer", "not anymore", "stopped",
      "quit" referencing the prior fact.
3. The two facts cannot describe coexisting realities (different
   qualifiers like seasons, scopes, etc.).

Use object identity AGGRESSIVELY for transfer-of-state events: any
prior fact whose object matches the new fact's object AND whose
predicate describes an ongoing relationship that the event would
naturally end is invalidated.

Examples that ARE contradictions (return their indexes):
- new: "Jordan sold the Honda Civic", prior: "Jordan drives the Honda
  Civic" -- selling ends driving.
- new: "Jordan moved the passport to the office desk drawer", prior:
  "Jordan keeps the passport in the blue safe" -- moving ends the old
  storage.
- new: "The vet cleared Pixel of the chicken allergy", prior: "Pixel
  is allergic to chicken" -- clearance ends the allergy.
- new: "Ava moved to Seattle in March", prior: "Ava lives in Denver"
  -- residence change.

Examples that are NOT contradictions (return ` + "`" + `[]` + "`" + `):
- new: "Michael is_brother_of Mark", prior: "Michael works_with Mark"
  (family vs employment -- different facts about same pair).
- new: "Alice likes pizza", prior: "Alice lives_in Berlin"
  (different domains, no shared object).
- new: "Bob is_named Robert", prior: "Bob works_at Acme"
  (different domains).
- new: "Jordan bought a Tesla", prior: "Jordan drives a Civic"
  (different objects -- the Civic is unaffected by the Tesla
  purchase; the Civic's status only changes if a separate
  sold/disposed claim is made).

When the new fact is itself an objective state (not an event) and
shares no object with the prior, return ` + "`" + `[]` + "`" + `.
`
