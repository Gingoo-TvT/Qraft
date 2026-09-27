package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Catalogs are supplied for one export only. They are not source code, global
// settings, or inputs to a model. IDs remain strings throughout the pipeline.
type OJTagCatalog struct {
	byID   map[string]ojTag
	byName map[string][]ojTag
	byPath map[string][]ojTag
}
type ojTag struct{ ID, Name, Path string }
type ojTagNode struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Status   int         `json:"status"`
	DelFlag  string      `json:"delFlag"`
	ParentID *string     `json:"parentId"`
	Children []ojTagNode `json:"children"`
}
type TestingExportOptions struct {
	TagCatalog *OJTagCatalog
	Numbering  *TestingExportNumbering
}
type TestingExportRequest struct {
	TagCatalog json.RawMessage `json:"tag_catalog"`
}

func ParseOJTagCatalog(data []byte) (*OJTagCatalog, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("validation: 标签目录不得超过 1 MiB")
	}
	var source struct {
		Active []ojTagNode `json:"activeTagsTree"`
	}
	if err := json.Unmarshal(data, &source); err != nil || source.Active == nil {
		return nil, fmt.Errorf("validation: 请上传含 activeTagsTree 的标签 JSON，标签 ID 必须为字符串")
	}
	catalog := &OJTagCatalog{byID: map[string]ojTag{}, byName: map[string][]ojTag{}, byPath: map[string][]ojTag{}}
	seen := map[string]bool{}
	count := 0
	var visit func([]ojTagNode, string, string, bool, int) error
	visit = func(nodes []ojTagNode, parentID, parentPath string, ancestorActive bool, depth int) error {
		if depth > 32 {
			return fmt.Errorf("validation: 标签层级超过 32 层")
		}
		for _, node := range nodes {
			count++
			if count > 10000 {
				return fmt.Errorf("validation: 标签目录超过 10000 项")
			}
			if node.ID == "" || len(node.ID) > 64 || strings.IndexFunc(node.ID, func(r rune) bool { return r < '0' || r > '9' }) >= 0 || strings.TrimSpace(node.Name) == "" || len([]rune(node.Name)) > 255 {
				return fmt.Errorf("validation: 标签 ID 或名称无效")
			}
			if seen[node.ID] {
				return fmt.Errorf("validation: 标签 ID 重复：%s", node.ID)
			}
			seen[node.ID] = true
			if (parentID != "" && (node.ParentID == nil || *node.ParentID != parentID)) || (parentID == "" && node.ParentID != nil && *node.ParentID != "" && *node.ParentID != "0") {
				return fmt.Errorf("validation: 标签 %s 的父级与树结构不一致", node.ID)
			}
			name := strings.TrimSpace(node.Name)
			path := name
			if parentPath != "" {
				path = parentPath + " / " + name
			}
			active := ancestorActive && node.Status == 1 && node.DelFlag == "0"
			if active {
				tag := ojTag{ID: node.ID, Name: name, Path: path}
				catalog.byID[tag.ID] = tag
				catalog.byName[tag.Name] = append(catalog.byName[tag.Name], tag)
				catalog.byPath[tag.Path] = append(catalog.byPath[tag.Path], tag)
			}
			if err := visit(node.Children, node.ID, path, active, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(source.Active, "", "", true, 0); err != nil {
		return nil, err
	}
	if len(catalog.byID) == 0 {
		return nil, fmt.Errorf("validation: 标签目录没有有效标签")
	}
	return catalog, nil
}

func (c *OJTagCatalog) resolve(tags []string) (ids []string, unresolved []string) {
	used := map[string]bool{}
	for _, original := range tags {
		value := strings.TrimSpace(original)
		if value == "" {
			continue
		}
		var matches []ojTag
		if c != nil {
			if direct, ok := c.byID[value]; ok {
				matches = []ojTag{direct}
			} else {
				// Prefer full path, then a unique exact name. No fuzzy or AI mapping.
				if strings.Contains(value, " / ") {
					matches = c.byPath[value]
				} else {
					// A root path is also an unqualified name. Do not let it win over
					// another node with that name deeper in the tree.
					matches = c.byName[value]
				}
			}
		}
		if len(matches) != 1 {
			unresolved = append(unresolved, original)
			continue
		}
		if !used[matches[0].ID] {
			ids = append(ids, matches[0].ID)
			used[matches[0].ID] = true
		}
	}
	return ids, unresolved
}
