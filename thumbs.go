package main

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
)

// PDChange is a manifest packagedefinition entry: a new partition, or an
// entity (brick) added to a partition.
type PDChange struct {
	Type          string `json:"type"` // "partition" or "entity"
	Name          string `json:"name"`
	Parent        string `json:"parent"`
	PartitionType string `json:"partitionType"`
	Partition     string `json:"partition"`
	Path          string `json:"path"`
}

// applyPD applies packagedefinition changes like SMF.
func applyPD(plain []byte, changes []PDChange) ([]byte, error) {
	for _, c := range changes {
		switch c.Type {
		case "partition":
			plain = append(plain, fmt.Sprintf("\r\n@partition name=%s parent=%s type=%s patchlevel=310\r\n", c.Name, c.Parent, c.PartitionType)...)
		case "entity":
			if bytes.Contains(plain, []byte(c.Path)) {
				continue
			}
			re, err := regexp.Compile(`@partition name=` + regexp.QuoteMeta(c.Partition) + ` parent=(.*?) type=(.*?) patchlevel=310\r\n`)
			if err != nil {
				return nil, err
			}
			loc := re.FindIndex(plain)
			if loc == nil {
				return nil, fmt.Errorf("no packagedefinition partition %q to add %s to", c.Partition, c.Path)
			}
			plain = append(plain[:loc[1]:loc[1]], append([]byte(c.Path+"\r\n"), plain[loc[1]:]...)...)
		default:
			return nil, fmt.Errorf("unknown packagedefinition change type %q", c.Type)
		}
	}
	return plain, nil
}

// thumbsPath is the engine config SMF patches for skipping the intro and
// for mods' thumbs lines.
func (cfg *Config) thumbsPath() string {
	return filepath.Join(cfg.abs(cfg.Game), "Retail", "thumbs.dat")
}

func (cfg *Config) thumbsBase() string { return cfg.abs(filepath.Join("clean", "thumbs.dat")) }

// writeThumbs patches thumbs.dat from its base: the file as it was before
// h3mod first changed it, which may already carry other tools' changes.
func writeThumbs(cfg *Config, skipIntro bool, lines []string) error {
	base, err := os.ReadFile(cfg.thumbsBase())
	if errors.Is(err, fs.ErrNotExist) {
		if base, err = os.ReadFile(cfg.thumbsPath()); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(cfg.thumbsBase()), 0o755); err != nil {
			return err
		}
		if err := writeFileAtomic(cfg.thumbsBase(), base); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	plain, err := decryptPD(base)
	if err != nil {
		return fmt.Errorf("thumbs.dat: %w", err)
	}
	plain = bytes.ReplaceAll(plain, []byte("\r\n"), []byte("\n"))
	if skipIntro {
		plain = bytes.Replace(plain, []byte("Boot.entity"), []byte("MainMenu.entity"), 1)
	}
	section := regexp.MustCompile(`(?i)\[Hitman5\]\n`)
	for _, l := range lines {
		loc := section.FindIndex(plain)
		if loc == nil {
			return errors.New("thumbs.dat has no [Hitman5] section")
		}
		plain = append(plain[:loc[1]:loc[1]], append([]byte(l+"\n"), plain[loc[1]:]...)...)
	}
	return writeFileAtomic(cfg.thumbsPath(), encryptPD(plain))
}

// restoreThumbs puts back thumbs.dat as it was before h3mod changed it.
func restoreThumbs(cfg *Config) error {
	base, err := os.ReadFile(cfg.thumbsBase())
	if errors.Is(err, fs.ErrNotExist) {
		return nil // never changed
	}
	if err != nil {
		return err
	}
	return writeFileAtomic(cfg.thumbsPath(), base)
}
