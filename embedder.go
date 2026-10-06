package surriti

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"math"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
	"regexp"
	"strings"
)

var embedTokenRE = regexp.MustCompile(`[\p{L}\p{N}_]+(?:[.-][\p{L}\p{N}_]+)*`)

type Embedder interface {
	EmbeddingDim() int
	Create(context.Context, string) ([]float64, error)
}

type BatchEmbedder interface {
	CreateBatch(context.Context, []string) ([][]float64, error)
}

func CreateBatch(ctx context.Context, e Embedder, input []string) ([][]float64, error) {
	if b, ok := e.(BatchEmbedder); ok {
		return b.CreateBatch(ctx, input)
	}
	return CreateBatchFallback(ctx, e, input)
}

type DummyEmbedder struct{ Dim int }

func NewDummyEmbedder(dim int) *DummyEmbedder {
	// The Python dummy constructor accepts the dimension verbatim. Production
	// configuration validation belongs to DriverConfig, not this test double.
	return &DummyEmbedder{Dim: dim}
}

func (d *DummyEmbedder) EmbeddingDim() int { return d.Dim }

func (d *DummyEmbedder) Create(_ context.Context, input string) ([]float64, error) {
	vec := make([]float64, d.Dim)
	tokens := embedTokenRE.FindAllString(strings.ToLower(input), -1)
	if len(tokens) == 0 {
		return vec, nil
	}
	for _, token := range tokens {
		digest := sha256.Sum256([]byte(token))
		i := int(binary.BigEndian.Uint32(digest[0:4]) % uint32(d.Dim))
		j := int(binary.BigEndian.Uint32(digest[4:8]) % uint32(d.Dim))
		vec[i] += 1
		vec[j] -= 1
	}
	var ss float64
	for _, x := range vec {
		ss += x * x
	}
	norm := math.Sqrt(ss)
	if norm < 1e-10 {
		return vec, nil
	}
	for i := range vec {
		vec[i] /= norm
	}
	return vec, nil
}

func (d *DummyEmbedder) CreateBatch(ctx context.Context, input []string) ([][]float64, error) {
	return CreateBatchFallback(ctx, d, input)
}

func CreateBatchFallback(ctx context.Context, e Embedder, input []string) ([][]float64, error) {
	out := make([][]float64, 0, len(input))
	for _, text := range input {
		v, err := e.Create(ctx, text)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func CosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// embedder.py uses zip(..., strict=False) for the dot product while norms
	// are computed over the complete vectors.
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var dot, aa, bb float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
	}
	for _, x := range a {
		aa += x * x
	}
	for _, x := range b {
		bb += x * x
	}
	if aa <= 0 || bb <= 0 {
		return 0
	}
	return dot / (math.Sqrt(aa) * math.Sqrt(bb))
}

// MemoryCosineSimilarity mirrors memory_retrieval.py, which intentionally
// rejects dimension-mismatched embeddings instead of using embedder.py's
// permissive zip semantics.
func MemoryCosineSimilarity(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	return CosineSimilarity(a, b)
}


type OpenAIEmbedder struct {
	Model string
	Dim int
	APIKey string
	BaseURL string
	HTTPClient *http.Client
}

func NewOpenAIEmbedder(model string, dim int, apiKey, baseURL string) (*OpenAIEmbedder, error) {
	if model == "" { model = "text-embedding-3-small" }
	if dim == 0 { dim = 1536 }
	if dim <= 0 { return nil, fmt.Errorf("%w: embedding dimension must be positive", ErrConfig) }
	if apiKey == "" { apiKey = os.Getenv("OPENAI_API_KEY") }
	if apiKey == "" { apiKey = "EMPTY" }
	if baseURL == "" { baseURL = os.Getenv("OPENAI_BASE_URL") }
	if baseURL == "" { baseURL = os.Getenv("OPENAI_API_BASE") }
	if baseURL == "" { baseURL = "https://api.openai.com/v1" }
	return &OpenAIEmbedder{Model:model,Dim:dim,APIKey:apiKey,BaseURL:strings.TrimRight(baseURL,"/"),HTTPClient:&http.Client{Timeout:60*time.Second}},nil
}

func (e *OpenAIEmbedder) EmbeddingDim() int { return e.Dim }

func (e *OpenAIEmbedder) Create(ctx context.Context, input string) ([]float64,error) {
	out,err:=e.CreateBatch(ctx,[]string{input})
	if err!=nil{return nil,err}
	if len(out)==0{return []float64{},nil}
	return out[0],nil
}

func (e *OpenAIEmbedder) CreateBatch(ctx context.Context, input []string) ([][]float64,error) {
	if len(input)==0{return [][]float64{},nil}
	payload:=map[string]any{"model":e.Model,"input":input,"dimensions":e.Dim}
	body,_:=json.Marshal(payload)
	req,err:=http.NewRequestWithContext(ctx,http.MethodPost,e.BaseURL+"/embeddings",bytes.NewReader(body));if err!=nil{return nil,err}
	req.Header.Set("Authorization","Bearer "+e.APIKey);req.Header.Set("Content-Type","application/json")
	client:=e.HTTPClient;if client==nil{client=http.DefaultClient}
	resp,err:=client.Do(req);if err!=nil{return nil,fmt.Errorf("%w: embedding API call failed: %v",ErrLLM,err)}
	defer resp.Body.Close()
	raw,_:=io.ReadAll(io.LimitReader(resp.Body,16<<20))
	if resp.StatusCode<200||resp.StatusCode>=300{return nil,fmt.Errorf("%w: embedding API call failed (%d): %s",ErrLLM,resp.StatusCode,strings.TrimSpace(string(raw)))}
	var parsed struct{Data []struct{Index int `json:"index"`;Embedding []float64 `json:"embedding"`} `json:"data"`}
	if err:=json.Unmarshal(raw,&parsed);err!=nil{return nil,fmt.Errorf("%w: malformed embedding response: %v",ErrLLM,err)}
	out:=make([][]float64,len(parsed.Data))
	for i,item:=range parsed.Data {
		idx:=item.Index
		if idx<0||idx>=len(out){idx=i}
		out[idx]=item.Embedding
	}
	return out,nil
}
