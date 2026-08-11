package keys

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

const fileWatchDebounce = 250 * time.Millisecond

type fileSource struct {
	name     string
	path     string
	watcher  *fsnotify.Watcher
	debounce time.Duration
}

func newFileSource(cfg *FileSourceConfig) (*fileSource, error) {
	source := &fileSource{
		name:     cfg.Name,
		path:     cfg.Path,
		debounce: fileWatchDebounce,
	}
	if !cfg.Watch {
		return source, nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("source '%s': create file watcher: %w", cfg.Name, err)
	}
	if err := watcher.Add(filepath.Dir(cfg.Path)); err != nil {
		watcher.Close()
		return nil, fmt.Errorf("source '%s': watch directory for '%s': %w", cfg.Name, cfg.Path, err)
	}
	source.watcher = watcher
	return source, nil
}

func (s *fileSource) Name() string {
	return s.name
}

func (s *fileSource) Load(_ context.Context) (LoadResult, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return LoadResult{}, fmt.Errorf("read key file: %w", err)
	}
	contribution, err := decodeYAMLContribution(data)
	if err != nil {
		return LoadResult{}, err
	}
	return Updated(contribution), nil
}

func (s *fileSource) Watch(ctx context.Context, notify func()) error {
	if s.watcher == nil {
		return nil
	}

	var debounce *time.Timer
	var debounceC <-chan time.Time
	stopDebounce := func() {
		if debounce == nil || debounce.Stop() {
			return
		}
		select {
		case <-debounce.C:
		default:
		}
	}
	defer stopDebounce()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err, ok := <-s.watcher.Errors:
			if !ok || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("watch key file: %w", err)
		case event, ok := <-s.watcher.Events:
			if !ok {
				return nil
			}
			if !s.relevant(event.Name) {
				continue
			}
			if debounce == nil {
				debounce = time.NewTimer(s.debounce)
			} else {
				stopDebounce()
				debounce.Reset(s.debounce)
			}
			debounceC = debounce.C
		case <-debounceC:
			debounceC = nil
			notify()
		}
	}
}

func (s *fileSource) relevant(eventPath string) bool {
	base := filepath.Base(eventPath)
	return base == filepath.Base(s.path) || base == "..data"
}

func (s *fileSource) close() error {
	if s.watcher == nil {
		return nil
	}
	return s.watcher.Close()
}
