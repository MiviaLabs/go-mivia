package projectregistry

import (
	"fmt"
	"path/filepath"
	"strings"
)

const projectGraphFilename = "mivialabs.lbug"
const projectSearchFilename = "mivialabs-search.sqlite"

func ProjectGraphStorageKey(projectID string) (string, error) {
	projectID = strings.TrimSpace(projectID)
	if !projectIDPattern.MatchString(projectID) {
		return "", fmt.Errorf("project graph storage id must match %s", projectIDPattern.String())
	}
	return projectID, nil
}

func ProjectGraphPath(baseLadybugPath string, projectID string) (string, error) {
	dir, _, err := projectStorageDir(baseLadybugPath, projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, projectGraphFilename), nil
}

func ProjectSearchPath(baseLadybugPath string, projectID string) (string, error) {
	dir, _, err := projectStorageDir(baseLadybugPath, projectID)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, projectSearchFilename), nil
}

func projectStorageDir(baseLadybugPath string, projectID string) (string, string, error) {
	if strings.TrimSpace(baseLadybugPath) == "" {
		return "", "", fmt.Errorf("base ladybug path must not be empty")
	}
	storageKey, err := ProjectGraphStorageKey(projectID)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(filepath.Dir(baseLadybugPath), "projects", storageKey), storageKey, nil
}
