package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

const adCache = "adblock-cache"
const routingCache = "route-cache"

type cachedRule struct {
	key      string
	data     []byte
	modified time.Time
	legacy   bool
}

func ruleCachePath() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "Slacker", "rules.db"), nil
}

func ruleCacheScope(config string) ([]byte, error) {
	p, err := filepath.Abs(config)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return []byte(p), err
}

// The two old directories are read-only migration sources. A cache miss
// must not create a database, a legacy directory, or an empty data file.
func readRuleCache(config, kind, key string, limit int) (cachedRule, error) {
	p, err := ruleCachePath()
	if err != nil {
		return cachedRule{}, err
	}
	scope, err := ruleCacheScope(config)
	if err != nil {
		return cachedRule{}, err
	}
	result := cachedRule{key: key}
	if _, err = os.Stat(p); err == nil {
		db, err := bolt.Open(p, 0600, &bolt.Options{ReadOnly: true, Timeout: 3 * time.Second})
		if err != nil {
			return result, err
		}
		err = db.View(func(tx *bolt.Tx) error {
			b := tx.Bucket(scope)
			if b == nil {
				return os.ErrNotExist
			}
			v := b.Get([]byte(kind + "/" + key))
			if v == nil {
				return os.ErrNotExist
			}
			if len(v) < 8 || len(v)-8 > limit {
				return fmt.Errorf("规则缓存记录无效")
			}
			result.modified = time.Unix(0, int64(binary.BigEndian.Uint64(v[:8])))
			result.data = bytes.Clone(v[8:])
			return nil
		})
		db.Close()
		if !errors.Is(err, os.ErrNotExist) {
			return result, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	f, err := os.Open(filepath.Join(filepath.Dir(config), kind, key))
	if err != nil {
		return result, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Size() > int64(limit) {
		return result, fmt.Errorf("旧规则缓存无效")
	}
	result.data, err = io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if len(result.data) > limit {
		return result, fmt.Errorf("旧规则缓存过大")
	}
	result.modified, result.legacy = info.ModTime(), true
	return result, err
}

func writeRuleCache(config, kind string, items []cachedRule) error {
	if len(items) == 0 {
		return nil
	}
	p, err := ruleCachePath()
	if err != nil {
		return err
	}
	scope, err := ruleCacheScope(config)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	db, err := bolt.Open(p, 0600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return err
	}
	defer db.Close()
	return db.Update(func(tx *bolt.Tx) error {
		b, err := tx.CreateBucketIfNotExists(scope)
		if err != nil {
			return err
		}
		for _, v := range items {
			key := []byte(kind + "/" + v.key)
			old := b.Get(key)
			// Never replace a newer download with an older migrated copy.
			if v.legacy && old != nil {
				continue
			}
			if kind == routingCache && len(old) >= 8 && bytes.Equal(old[8:], v.data) {
				continue
			}
			raw := make([]byte, 8+len(v.data))
			binary.BigEndian.PutUint64(raw[:8], uint64(v.modified.UnixNano()))
			copy(raw[8:], v.data)
			if err = b.Put(key, raw); err != nil {
				return err
			}
		}
		return nil
	})
}
