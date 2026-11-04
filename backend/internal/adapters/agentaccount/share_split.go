package agentaccount

import (
	"os"
	"path/filepath"
)

var _ interface{ HistorySplit(string) []string } = (*Claude)(nil)

// HistorySplit names the shared folders home keeps its own non-empty copy of
// while the computer's own folder has one too. Linking leaves such a copy in
// place rather than lose it, so conversations in it are not seen by other
// accounts. It changes nothing.
func (c *Claude) HistorySplit(home string) []string {
	if home == "" {
		return nil
	}
	return splitDirs(home, c.DeviceHome, claudeSharedDirs)
}

// splitDirs lists, in order, the names that are real directories holding
// something in home, where shared also has that name. A link, an empty
// directory, or an entry only the account has (linking moves it to shared) is
// not a split.
func splitDirs(home, shared string, names []string) []string {
	var split []string
	for _, name := range names {
		info, err := os.Lstat(filepath.Join(home, name))
		if err != nil || !info.IsDir() || emptyDir(filepath.Join(home, name)) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(shared, name)); err != nil {
			continue
		}
		split = append(split, name)
	}
	return split
}
