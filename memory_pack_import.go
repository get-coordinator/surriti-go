package surriti

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"

	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func readJSONL(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []map[string]any{}, nil
		}
		return nil, err
	}
	defer f.Close()

	rows := []map[string]any{}
	scanner := bufio.NewScanner(f)
	// Memory-pack rows can contain summaries/attributes substantially larger
	// than Scanner's 64 KiB default. Bound a single row generously while still
	// preventing unbounded allocation on malformed input.
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var row map[string]any
		if err := decodeJSONNumbers([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("Invalid JSON in %s line %d: %w", filepath.Base(path), lineNo, err)
		}
		if row == nil {
			return nil, fmt.Errorf("invalid row in %s line %d: expected object", filepath.Base(path), lineNo)
		}
		rows = append(rows, row)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}
func restorePackDatetimes(row map[string]any) map[string]any {
	for k, v := range row {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if len(s) < 19 {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, strings.Replace(s, " ", "T", 1)); err == nil {
			row[k] = t
		}
	}
	return row
}

func withoutOmissionMarkers(row map[string]any) map[string]any {
	out := cloneMap(row)
	delete(out, "source_episode_omitted")
	delete(out, "episodes_omitted")
	delete(out, "source_episode_count")
	return out
}

func stableImportUUID(targetGroup, table string, source any, fallback string) string {
	s := strings.TrimSpace(stringFromAny(source))
	if s == "" {
		s = strings.TrimSpace(fallback)
	}
	if s == "" {
		s = strings.ReplaceAll(newUUID(), "-", "")
	}
	name := "surriti:" + targetGroup + ":" + table + ":" + s
	ns, _ := hex.DecodeString("6ba7b8119dad11d180b400c04fd430c8")
	h := sha1.New()
	h.Write(ns)
	h.Write([]byte(name))
	sum := h.Sum(nil)[:16]
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		sum[0:4], sum[4:6], sum[6:8], sum[8:10], sum[10:16])
}

func originStamp(manifest map[string]any, at time.Time) map[string]any {
	stamp := map[string]any{"imported_at": at.Format(time.RFC3339Nano)}
	if source := mapFromAny(manifest["source"]); source != nil {
		if g := stringFromAny(source["group_id"]); g != "" {
			stamp["source_group_id"] = g
		}
	}
	if created := manifest["created_at"]; created != nil && stringFromAny(created) != "" {
		stamp["pack_created_at"] = created
	}
	return stamp
}

func withOrigin(row map[string]any, stamp map[string]any) map[string]any {
	attrs := cloneMap(mapFromAny(row["attributes"]))
	attrs["origin"] = cloneMap(stamp)
	row["attributes"] = attrs
	return row
}

func existingRowsByKey(ctx context.Context, driver Queryer, table, groupID, field string, values []string) (map[string]map[string]any, error) {
	if len(values) == 0 {
		return map[string]map[string]any{}, nil
	}
	q := fmt.Sprintf("SELECT * FROM %s WHERE group_id = $g AND %s IN $values;", table, field)
	raw, err := driver.Query(ctx, q, map[string]any{"g": groupID, "values": values})
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]any{}
	for _, row := range UnwrapRows(raw) {
		key := stringFromAny(row[field])
		if key != "" {
			if _, ok := out[key]; !ok {
				out[key] = row
			}
		}
	}
	return out, nil
}

func deletePortableGroup(ctx context.Context, driver Queryer, groupID string) error {
	for _, table := range []string{"relates_to", "entity_alias", "relation_frame", "entity"} {
		if _, err := driver.Query(ctx, "DELETE "+table+" WHERE group_id = $g;", map[string]any{"g": groupID}); err != nil {
			return err
		}
	}
	return nil
}

func upsertPlainPackRecord(ctx context.Context, driver Queryer, table string, row map[string]any) error {
	row = restorePackDatetimes(row)
	_, err := driver.Query(ctx, fmt.Sprintf("UPSERT type::record(%q, $uuid) CONTENT $row;", table), map[string]any{"uuid": row["uuid"], "row": row})
	return err
}

func safePackUpsert(ctx context.Context, driver Queryer, table string, row map[string]any, label string, warnings *[]string) error {
	err := upsertPlainPackRecord(ctx, driver, table, row)
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "already contains") || strings.Contains(msg, "unique") {
		*warnings = append(*warnings, "Race on "+label+"; reusing existing row.")
		return nil
	}
	return err
}

func resolveFrameRef(raw any, m map[string]string, sourceUUID string, warnings *[]string) *string {
	id := strings.TrimSpace(stringFromAny(raw))
	if id == "" {
		return nil
	}
	if mapped, ok := m[id]; ok {
		return &mapped
	}
	*warnings = append(*warnings, fmt.Sprintf("Edge %q: relation frame %q not mapped — cleared", sourceUUID, id))
	return nil
}

func upsertPackEdge(ctx context.Context, driver Queryer, row map[string]any) error {
	src := stringFromAny(row["source_node_uuid"])
	tgt := stringFromAny(row["target_node_uuid"])
	delete(row, "source_node_uuid")
	delete(row, "target_node_uuid")
	row = restorePackDatetimes(row)
	raw, err := driver.Query(ctx, "SELECT uuid FROM relates_to WHERE group_id = $group_id AND uuid = $uuid LIMIT 1;", map[string]any{"group_id": row["group_id"], "uuid": row["uuid"]})
	if err != nil {
		return err
	}
	if len(UnwrapRows(raw)) > 0 {
		_, err = driver.Query(ctx, "UPDATE relates_to CONTENT $row WHERE uuid = $uuid;", map[string]any{"uuid": row["uuid"], "row": row})
		return err
	}
	_, err = driver.Query(ctx, `RELATE (type::record("entity", $src))->relates_to->(type::record("entity", $tgt)) CONTENT $row;`, map[string]any{"src": src, "tgt": tgt, "row": row})
	return err
}

func ImportGroupFromDir(ctx context.Context, driver Queryer, inputDir, targetGroupID, mode string) (ImportResult, error) {
	if mode == "" {
		mode = "merge"
	}
	if mode != "merge" && mode != "replace" {
		return ImportResult{}, fmt.Errorf("mode must be 'merge' or 'replace'")
	}
	if mode == "replace" && !destructiveAllowed() {
		return ImportResult{}, &destructiveDisabledError{operation: "Memory Pack import mode='replace'"}
	}
	validation := ValidatePackDir(inputDir)
	if !validation.OK {
		return ImportResult{TargetGroupID: targetGroupID, Mode: mode, Counts: map[string]int{}, Validation: validation, Warnings: []string{}}, nil
	}
	entities, err := readJSONL(filepath.Join(inputDir, "entities.jsonl"))
	if err != nil {
		return ImportResult{}, err
	}
	aliases, err := readJSONL(filepath.Join(inputDir, "entity_aliases.jsonl"))
	if err != nil {
		return ImportResult{}, err
	}
	frames, err := readJSONL(filepath.Join(inputDir, "relation_frames.jsonl"))
	if err != nil {
		return ImportResult{}, err
	}
	edges, err := readJSONL(filepath.Join(inputDir, "edges.jsonl"))
	if err != nil {
		return ImportResult{}, err
	}
	var manifest map[string]any
	_ = readJSONFile(filepath.Join(inputDir, "manifest.json"), &manifest)
	origin := originStamp(manifest, utcNow())
	if mode == "replace" {
		if err := deletePortableGroup(ctx, driver, targetGroupID); err != nil {
			return ImportResult{}, err
		}
	}
	names := []string{}
	for _, r := range entities {
		if n := stringFromAny(r["name"]); n != "" {
			names = append(names, n)
		}
	}
	frameNames := []string{}
	for _, r := range frames {
		if n := stringFromAny(r["canonical_name"]); n != "" {
			frameNames = append(frameNames, n)
		}
	}
	aliasNames := []string{}
	for _, r := range aliases {
		if n := stringFromAny(r["normalized_alias"]); n != "" {
			aliasNames = append(aliasNames, n)
		}
	}
	existingEntities, err := existingRowsByKey(ctx, driver, "entity", targetGroupID, "name", names)
	if err != nil {
		return ImportResult{}, err
	}
	existingFrames, err := existingRowsByKey(ctx, driver, "relation_frame", targetGroupID, "canonical_name", frameNames)
	if err != nil {
		return ImportResult{}, err
	}
	existingAliases, err := existingRowsByKey(ctx, driver, "entity_alias", targetGroupID, "normalized_alias", aliasNames)
	if err != nil {
		return ImportResult{}, err
	}
	entityMap := map[string]string{}
	frameMap := map[string]string{}
	edgeMap := map[string]string{}
	counts := map[string]int{"entities": 0, "entity_aliases": 0, "relation_frames": 0, "edges": 0}
	warnings := []string{}

	for _, source := range entities {
		sourceUUID := stringFromAny(source["uuid"])
		name := stringFromAny(source["name"])
		existingUUID := ""
		if r := existingEntities[name]; r != nil {
			existingUUID = stringFromAny(r["uuid"])
		}
		target := existingUUID
		if target == "" {
			target = stableImportUUID(targetGroupID, "entity", sourceUUID, name)
		}
		entityMap[sourceUUID] = target
		if sourceUUID == "" {
			warnings = append(warnings, fmt.Sprintf("Entity %q has empty source uuid; import may not be idempotent across runs.", name))
		}
		row := withoutOmissionMarkers(source)
		if row["created_at"] == nil {
			row["created_at"] = utcNow()
			warnings = append(warnings, fmt.Sprintf("Entity %q missing created_at; defaulting to now.", name))
		}
		row["uuid"] = target
		row["group_id"] = targetGroupID
		if existingUUID == "" {
			withOrigin(row, origin)
		}
		if err := safePackUpsert(ctx, driver, "entity", row, fmt.Sprintf("entity %q", name), &warnings); err != nil {
			return ImportResult{}, err
		}
		counts["entities"]++
	}
	for _, source := range frames {
		sourceUUID := stringFromAny(source["uuid"])
		canonical := stringFromAny(source["canonical_name"])
		target := ""
		if r := existingFrames[canonical]; r != nil {
			target = stringFromAny(r["uuid"])
		}
		if target == "" {
			target = stableImportUUID(targetGroupID, "relation_frame", sourceUUID, canonical)
		}
		frameMap[sourceUUID] = target
		row := withoutOmissionMarkers(source)
		if row["created_at"] == nil {
			row["created_at"] = utcNow()
		}
		row["uuid"] = target
		row["group_id"] = targetGroupID
		if err := safePackUpsert(ctx, driver, "relation_frame", row, fmt.Sprintf("frame %q", canonical), &warnings); err != nil {
			return ImportResult{}, err
		}
		counts["relation_frames"]++
	}
	for _, source := range aliases {
		normalized := stringFromAny(source["normalized_alias"])
		oldEntity := stringFromAny(source["entity_uuid"])
		mapped := entityMap[oldEntity]
		if mapped == "" {
			warnings = append(warnings, fmt.Sprintf("Skipped alias %q: missing entity %q", source["uuid"], oldEntity))
			continue
		}
		target := ""
		if r := existingAliases[normalized]; r != nil {
			target = stringFromAny(r["uuid"])
		}
		if target == "" {
			target = stableImportUUID(targetGroupID, "entity_alias", source["uuid"], normalized)
		}
		row := withoutOmissionMarkers(source)
		if row["created_at"] == nil {
			row["created_at"] = utcNow()
		}
		row["uuid"] = target
		row["group_id"] = targetGroupID
		row["entity_uuid"] = mapped
		row["source_episode_uuid"] = nil
		if err := safePackUpsert(ctx, driver, "entity_alias", row, fmt.Sprintf("alias %q", normalized), &warnings); err != nil {
			return ImportResult{}, err
		}
		counts["entity_aliases"]++
	}
	edgeFactKeys := map[string]string{}
	for _, source := range edges {
		sourceUUID := stringFromAny(source["uuid"])
		src := entityMap[stringFromAny(source["source_node_uuid"])]
		tgt := entityMap[stringFromAny(source["target_node_uuid"])]
		edgeName := stringFromAny(source["canonical_name"])
		if edgeName == "" {
			edgeName = stringFromAny(source["name"])
		}
		if src != "" && tgt != "" {
			key := MakeFactKey(targetGroupID, src, edgeName, tgt, "")
			edgeFactKeys[sourceUUID] = key
			edgeMap[sourceUUID] = stableImportUUID(targetGroupID, "relates_to", key, "")
		} else {
			edgeMap[sourceUUID] = stableImportUUID(targetGroupID, "relates_to", sourceUUID, "")
		}
	}
	values := []string{}
	for _, k := range edgeFactKeys {
		values = append(values, k)
	}
	existingEdges, err := existingRowsByKey(ctx, driver, "relates_to", targetGroupID, "fact_key", values)
	if err != nil {
		return ImportResult{}, err
	}
	preserved := map[string]struct{}{}
	for sourceUUID, key := range edgeFactKeys {
		if r := existingEdges[key]; r != nil {
			eid := stringFromAny(r["uuid"])
			if eid != "" && eid != edgeMap[sourceUUID] {
				edgeMap[sourceUUID] = eid
				preserved[sourceUUID] = struct{}{}
			}
		}
	}
	for _, source := range edges {
		sourceUUID := stringFromAny(source["uuid"])
		src := entityMap[stringFromAny(source["source_node_uuid"])]
		tgt := entityMap[stringFromAny(source["target_node_uuid"])]
		if src == "" || tgt == "" {
			warnings = append(warnings, fmt.Sprintf("Skipped edge %q: missing endpoint(s)", sourceUUID))
			continue
		}
		if _, ok := preserved[sourceUUID]; ok {
			warnings = append(warnings, fmt.Sprintf("Preserved existing target fact for imported edge %q.", sourceUUID))
			counts["edges"]++
			continue
		}
		sourceEpisodeCount := intFromAny(source["source_episode_count"])
		row := withoutOmissionMarkers(source)
		target := edgeMap[sourceUUID]
		edgeName := stringFromAny(row["canonical_name"])
		if edgeName == "" {
			edgeName = stringFromAny(row["name"])
		}
		row["uuid"] = target
		row["group_id"] = targetGroupID
		row["source_node_uuid"] = src
		row["target_node_uuid"] = tgt
		row["episodes"] = []string{}
		row["relation_frame_id"] = resolveFrameRef(row["relation_frame_id"], frameMap, sourceUUID, &warnings)
		mappedSup := []string{}
		for _, e := range asStringSlice(row["supersedes"]) {
			if m := edgeMap[e]; m != "" {
				mappedSup = append(mappedSup, m)
			}
		}
		row["supersedes"] = mappedSup
		if sb := stringFromAny(row["superseded_by"]); sb != "" {
			if m := edgeMap[sb]; m != "" {
				row["superseded_by"] = m
			} else {
				row["superseded_by"] = nil
			}
		} else {
			row["superseded_by"] = nil
		}
		edgeOrigin := cloneMap(origin)
		edgeOrigin["source_type"] = stringFromAny(row["source_type"])
		if edgeOrigin["source_type"] == "" {
			edgeOrigin["source_type"] = "unknown"
		}
		if sourceEpisodeCount > 0 {
			edgeOrigin["source_episode_count"] = sourceEpisodeCount
		}
		withOrigin(row, edgeOrigin)
		row["source_type"] = "imported"
		row["fact_key"] = MakeFactKey(targetGroupID, src, edgeName, tgt, "")
		if err := upsertPackEdge(ctx, driver, row); err != nil {
			return ImportResult{}, err
		}
		counts["edges"]++
	}
	return ImportResult{TargetGroupID: targetGroupID, Mode: mode, Counts: counts, Validation: validation, Warnings: warnings}, nil
}

func ImportGroupFromZip(ctx context.Context, driver Queryer, inputPath, targetGroupID, mode string) (ImportResult, error) {
	validation := ValidatePackZip(inputPath)
	if !validation.OK {
		return ImportResult{TargetGroupID: targetGroupID, Mode: mode, Counts: map[string]int{}, Validation: validation, Warnings: []string{}}, nil
	}
	zr, err := zip.OpenReader(inputPath)
	if err != nil {
		return ImportResult{}, err
	}
	defer zr.Close()
	tmp, err := os.MkdirTemp("", "surriti_pack_import_")
	if err != nil {
		return ImportResult{}, err
	}
	defer os.RemoveAll(tmp)
	if err := extractPackZip(zr, tmp); err != nil {
		return ImportResult{}, err
	}
	return ImportGroupFromDir(ctx, driver, tmp, targetGroupID, mode)
}
