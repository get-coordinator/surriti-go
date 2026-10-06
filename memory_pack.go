package surriti

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	MemoryPackFormat = "surriti.memory-pack"
	MemoryPackVersion = 1
	maxPackUncompressedBytes int64 = 512 * 1024 * 1024
)

var memoryPackFiles = []string{"manifest.json","checksums.json","entities.jsonl","entity_aliases.jsonl","relation_frames.jsonl","edges.jsonl"}

type ValidationResult struct {
	OK bool `json:"ok"`
	Errors []string `json:"errors"`
	Warnings []string `json:"warnings"`
	Counts map[string]int `json:"counts"`
}

type ExportResult struct {
	OutputPath string `json:"output_path"`
	Manifest map[string]any `json:"manifest"`
	Counts map[string]int `json:"counts"`
	Checksums map[string]map[string]any `json:"checksums"`
	Warnings []string `json:"warnings"`
}

type ImportResult struct {
	TargetGroupID string `json:"target_group_id"`
	Mode string `json:"mode"`
	Counts map[string]int `json:"counts"`
	Validation ValidationResult `json:"validation"`
	Warnings []string `json:"warnings"`
}

func packJSONSafe(v any) any {
	if v==nil{return nil}
	switch x:=v.(type){
	case string,bool,int,int8,int16,int32,int64,uint,uint8,uint16,uint32,uint64,float32,float64,json.Number:
		return x
	case time.Time:return x.Format(time.RFC3339Nano)
	case *time.Time:if x==nil{return nil};return x.Format(time.RFC3339Nano)
	case []byte:return string(x)
	case []string:
		out:=make([]any,len(x));for i,v:=range x{out[i]=v};return out
	case []any:
		out:=make([]any,len(x));for i,v:=range x{out[i]=packJSONSafe(v)};return out
	case map[string]any:
		out:=map[string]any{};for k,v:=range x{out[k]=packJSONSafe(v)};return out
	default:
		return stringFromAny(v)
	}
}

func normalizePackRow(row map[string]any, table, includeEmbeddings string) map[string]any {
	out:=map[string]any{}
	if table=="relates_to"{
		src:=stringFromAny(row["source_node_uuid"]);if src==""{src=stripRecordID(row["in"])}
		tgt:=stringFromAny(row["target_node_uuid"]);if tgt==""{tgt=stripRecordID(row["out"])}
		if src!=""{out["source_node_uuid"]=src};if tgt!=""{out["target_node_uuid"]=tgt}
	}
	for k,v:=range row{
		if k=="id"||(table=="relates_to"&&(k=="in"||k=="out"||k=="source_node_uuid"||k=="target_node_uuid")){continue}
		if table=="entity_alias"&&k=="source_episode_uuid"{
			if v!=nil&&stringFromAny(v)!=""{out["source_episode_omitted"]=true};continue
		}
		if table=="relates_to"&&k=="episodes"{
			eps:=asStringSlice(v);out["source_episode_count"]=len(eps);out["episodes_omitted"]=len(eps)>0;continue
		}
		if includeEmbeddings=="never"{
			if table=="entity"&&(k=="name_embedding"||k=="profile_embedding"||k=="emb"){continue}
			if table=="relates_to"&&(k=="fact_embedding"||k=="emb"){continue}
		}
		out[k]=packJSONSafe(v)
	}
	if table=="relates_to"{
		if _,ok:=out["source_episode_count"];!ok{out["source_episode_count"]=0;out["episodes_omitted"]=false}
	}
	return out
}

var packFields=map[string]string{
	"entity": "uuid, group_id, name, summary, labels, attributes, name_embedding, created_at, canonical_name, aliases, profile_summary, profile_embedding, salience, mention_count, last_seen_at, merged_into, traits, goals_active, domain",
	"entity_alias": "uuid, group_id, alias, normalized_alias, entity_uuid, confidence, source_episode_uuid, created_at",
	"relation_frame": "uuid, group_id, canonical_name, aliases, description, directionality, temporal_kind, cardinality, contradiction_policy, inverse_name, subject_role, object_role, confidence, created_at",
	"relates_to": "uuid, group_id, name, fact, fact_embedding, episodes, valid_at, invalid_at, expired_at, attributes, created_at, status, polarity, source_type, confidence, temporal, singleton, domain, supersedes, superseded_by, fact_key, relation_frame_id, canonical_name, qualifiers, roles, conflict_group_id, derived, derived_from, weight, reinforcement_count, last_reinforced_at, recall_count, last_recalled_at, decay_score, stability, valence, intensity, consolidates, is_belief, belief_holder",
}

func paginatePack(ctx context.Context, driver Queryer, table, groupID string, pageSize int) ([]map[string]any,error) {
	if pageSize<=0{pageSize=1000}
	all:=[]map[string]any{};offset:=0
	for{
		extra:="";if table=="relates_to"{extra=", record::id(in) AS source_node_uuid, record::id(out) AS target_node_uuid"}
		q:=fmt.Sprintf("SELECT %s%s FROM %s WHERE group_id = $g ORDER BY created_at, uuid LIMIT $limit START $offset;",packFields[table],extra,table)
		raw,err:=driver.Query(ctx,q,map[string]any{"g":groupID,"limit":pageSize,"offset":offset});if err!=nil{return nil,err}
		rows:=UnwrapRows(raw);if len(rows)==0{break};all=append(all,rows...)
		if len(rows)<pageSize{break};offset+=pageSize
	}
	return all,nil
}

func writeJSONL(path string, rows []map[string]any) error {
	f,err:=os.Create(path);if err!=nil{return err};defer f.Close()
	w:=bufio.NewWriter(f);defer w.Flush()
	enc:=json.NewEncoder(w);enc.SetEscapeHTML(false)
	for _,row:=range rows{if err:=enc.Encode(row);err!=nil{return err}}
	return nil
}

func fileSHA256(path string)(string,error){
	f,err:=os.Open(path);if err!=nil{return "",err};defer f.Close()
	h:=sha256.New();if _,err:=io.Copy(h,f);err!=nil{return "",err};return hex.EncodeToString(h.Sum(nil)),nil
}

func ExportGroupToDir(ctx context.Context,driver Queryer,groupID,outputDir,includeEmbeddings string,pageSize int,embeddingModel *string)(ExportResult,error){
	warnings:=[]string{}
	if includeEmbeddings!="never"&&includeEmbeddings!="auto"&&includeEmbeddings!="always"{warnings=append(warnings,fmt.Sprintf("Unknown include_embeddings value %q; falling back to 'never'.",includeEmbeddings));includeEmbeddings="never"}
	if includeEmbeddings==""{includeEmbeddings="never"}
	if err:=os.MkdirAll(outputDir,0755);err!=nil{return ExportResult{},err}
	started:=utcNow()
	tables:=[]struct{table,file,key string}{{"entity","entities.jsonl","entities"},{"entity_alias","entity_aliases.jsonl","entity_aliases"},{"relation_frame","relation_frames.jsonl","relation_frames"},{"relates_to","edges.jsonl","edges"}}
	counts:=map[string]int{};checksums:=map[string]map[string]any{}
	for _,spec:=range tables{
		rows,err:=paginatePack(ctx,driver,spec.table,groupID,pageSize);if err!=nil{return ExportResult{},err}
		norm:=make([]map[string]any,len(rows));for i,row:=range rows{norm[i]=normalizePackRow(row,spec.table,includeEmbeddings)}
		path:=filepath.Join(outputDir,spec.file);if err:=writeJSONL(path,norm);err!=nil{return ExportResult{},err}
		counts[spec.key]=len(norm);sum,err:=fileSHA256(path);if err!=nil{return ExportResult{},err}
		checksums[spec.file]=map[string]any{"rows":len(norm),"sha256":sum}
	}
	finished:=utcNow()
	var model any;if embeddingModel!=nil{model=*embeddingModel}
	var dim any;if d,ok:=driver.(*SurrealDriver);ok{dim=d.Config().EmbeddingDim}
	manifest:=map[string]any{
		"format":MemoryPackFormat,"version":MemoryPackVersion,"created_at":finished.Format(time.RFC3339Nano),
		"export_started_at":started.Format(time.RFC3339Nano),"export_finished_at":finished.Format(time.RFC3339Nano),"consistency":"best_effort",
		"source":map[string]any{"group_id":groupID,"surriti_version":Version,"embedding_model":model,"embedding_dim":dim},
		"policy":map[string]any{"mode":"portable_graph","include_episodes":false,"include_mentions":false,"include_embeddings":includeEmbeddings,"include_communities":false},
		"counts":counts,"privacy":map[string]any{"contains_user_memory":true,"contains_raw_transcripts":false,"contains_embeddings":includeEmbeddings!="never"},
		"files":map[string]any{"entities":"entities.jsonl","entity_aliases":"entity_aliases.jsonl","relation_frames":"relation_frames.jsonl","edges":"edges.jsonl"},
	}
	for name,value:=range map[string]any{"manifest.json":manifest,"checksums.json":checksums}{
		f,err:=os.Create(filepath.Join(outputDir,name));if err!=nil{return ExportResult{},err}
		enc:=json.NewEncoder(f);enc.SetEscapeHTML(false);enc.SetIndent("","  ");err=enc.Encode(value);cerr:=f.Close();if err!=nil{return ExportResult{},err};if cerr!=nil{return ExportResult{},cerr}
	}
	return ExportResult{OutputPath:outputDir,Manifest:manifest,Counts:counts,Checksums:checksums,Warnings:warnings},nil
}

func ExportGroupToZip(ctx context.Context,driver Queryer,groupID,outputPath,includeEmbeddings string,pageSize int,embeddingModel *string)(ExportResult,error){
	tmpDir,err:=os.MkdirTemp("","surriti_pack_");if err!=nil{return ExportResult{},err};defer os.RemoveAll(tmpDir)
	result,err:=ExportGroupToDir(ctx,driver,groupID,tmpDir,includeEmbeddings,pageSize,embeddingModel);if err!=nil{return ExportResult{},err}
	tmpPath:=outputPath+".tmp";_ = os.Remove(tmpPath)
	f,err:=os.Create(tmpPath);if err!=nil{return ExportResult{},err};zw:=zip.NewWriter(f)
	for _,name:=range memoryPackFiles{
		path:=filepath.Join(tmpDir,name);src,err:=os.Open(path);if err!=nil{zw.Close();f.Close();return ExportResult{},err}
		w,err:=zw.Create(name);if err==nil{_,err=io.Copy(w,src)};src.Close();if err!=nil{zw.Close();f.Close();return ExportResult{},err}
	}
	if err:=zw.Close();err!=nil{f.Close();return ExportResult{},err};if err:=f.Close();err!=nil{return ExportResult{},err}
	if err:=os.Rename(tmpPath,outputPath);err!=nil{return ExportResult{},err};result.OutputPath=outputPath;return result,nil
}

func validatePackManifest(manifest map[string]any) []string {
	errs:=[]string{}
	if stringFromAny(manifest["format"])!=MemoryPackFormat{errs=append(errs,fmt.Sprintf("Unsupported format %q; expected %q",stringFromAny(manifest["format"]),MemoryPackFormat))}
	if intFromAny(manifest["version"])<1{errs=append(errs,fmt.Sprintf("Unknown pack version %v",manifest["version"]))}
	return errs
}

func readJSONFile(path string,out any)error{b,err:=os.ReadFile(path);if err!=nil{return err};return json.Unmarshal(b,out)}

func ValidatePackDir(path string) ValidationResult {
	res:=ValidationResult{Warnings:[]string{},Counts:map[string]int{}}
	for _,name:=range memoryPackFiles{if _,err:=os.Stat(filepath.Join(path,name));err!=nil{res.Errors=append(res.Errors,"Missing required file: "+name)}}
	if len(res.Errors)>0{return res}
	var manifest map[string]any;if err:=readJSONFile(filepath.Join(path,"manifest.json"),&manifest);err!=nil{res.Errors=append(res.Errors,"Cannot read manifest.json: "+err.Error());return res}
	res.Errors=append(res.Errors,validatePackManifest(manifest)...)
	var checks map[string]map[string]any;if err:=readJSONFile(filepath.Join(path,"checksums.json"),&checks);err!=nil{res.Errors=append(res.Errors,"Cannot read checksums.json: "+err.Error());return res}
	for name,expected:=range checks{
		p:=filepath.Join(path,name);sum,err:=fileSHA256(p);if err!=nil{res.Errors=append(res.Errors,fmt.Sprintf("Checksum entry %q references missing file",name));continue}
		want:=stringFromAny(expected["sha256"]);if sum!=want{res.Errors=append(res.Errors,fmt.Sprintf("Checksum mismatch for %s: expected %.16s..., got %.16s...",name,want,sum))}
		res.Counts[strings.TrimSuffix(name,".jsonl")]=intFromAny(expected["rows"])
	}
	res.OK=len(res.Errors)==0;return res
}

func ValidatePackZip(path string) ValidationResult {
	res:=ValidationResult{Warnings:[]string{},Counts:map[string]int{}}
	zr,err:=zip.OpenReader(path);if err!=nil{if _,stat:=os.Stat(path);stat!=nil{res.Errors=append(res.Errors,"File not found: "+path)}else{res.Errors=append(res.Errors,"Not a valid ZIP file: "+path)};return res};defer zr.Close()
	byName:=map[string]*zip.File{};for _,f:=range zr.File{byName[f.Name]=f}
	for _,name:=range memoryPackFiles{if byName[name]==nil{res.Errors=append(res.Errors,"Missing required file in ZIP: "+name)}}
	if len(res.Errors)>0{return res}
	readZip:=func(name string)([]byte,error){rc,err:=byName[name].Open();if err!=nil{return nil,err};defer rc.Close();return io.ReadAll(rc)}
	var manifest map[string]any;b,err:=readZip("manifest.json");if err!=nil||json.Unmarshal(b,&manifest)!=nil{res.Errors=append(res.Errors,"Cannot read manifest.json from ZIP");return res};res.Errors=append(res.Errors,validatePackManifest(manifest)...)
	var checks map[string]map[string]any;b,err=readZip("checksums.json");if err!=nil||json.Unmarshal(b,&checks)!=nil{res.Errors=append(res.Errors,"Cannot read checksums.json from ZIP");return res}
	for name,expected:=range checks{
		raw,err:=readZip(name);if err!=nil{res.Errors=append(res.Errors,fmt.Sprintf("Checksum entry %q references missing file in ZIP",name));continue}
		sum:=sha256.Sum256(raw);actual:=hex.EncodeToString(sum[:]);want:=stringFromAny(expected["sha256"]);if actual!=want{res.Errors=append(res.Errors,fmt.Sprintf("Checksum mismatch for %s: expected %.16s..., got %.16s...",name,want,actual))}
		res.Counts[strings.TrimSuffix(name,".jsonl")]=intFromAny(expected["rows"])
	}
	res.OK=len(res.Errors)==0;return res
}

func validatePackZipSafety(zr *zip.ReadCloser,root string)error{
	var total int64;cleanRoot,err:=filepath.Abs(root);if err!=nil{return err}
	for _,f:=range zr.File{
		if f.FileInfo().IsDir(){continue};total+=int64(f.UncompressedSize64);if total>maxPackUncompressedBytes{return fmt.Errorf("Zip uncompressed size %d exceeds limit %d; possible zip bomb",total,maxPackUncompressedBytes)}
		target,err:=filepath.Abs(filepath.Join(root,f.Name));if err!=nil{return err}
		if target!=cleanRoot&&!strings.HasPrefix(target,cleanRoot+string(os.PathSeparator)){return fmt.Errorf("Zip entry %q escapes temp dir; possible path-traversal attack",f.Name)}
	}
	return nil
}

func extractPackZip(zr *zip.ReadCloser,root string)error{
	if err:=validatePackZipSafety(zr,root);err!=nil{return err}
	for _,f:=range zr.File{
		target:=filepath.Join(root,f.Name)
		if f.FileInfo().IsDir(){if err:=os.MkdirAll(target,0755);err!=nil{return err};continue}
		if err:=os.MkdirAll(filepath.Dir(target),0755);err!=nil{return err}
		rc,err:=f.Open();if err!=nil{return err};dst,err:=os.Create(target);if err!=nil{rc.Close();return err}
		_,copyErr:=io.Copy(dst,rc);closeErr:=dst.Close();rc.Close();if copyErr!=nil{return copyErr};if closeErr!=nil{return closeErr}
	}
	return nil
}
