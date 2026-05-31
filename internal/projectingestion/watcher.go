package projectingestion

import (
	"errors"
	"os"

	"github.com/fsnotify/fsnotify"
)

type WatchOp uint32

const (
	WatchCreate WatchOp = 1 << iota
	WatchWrite
	WatchRemove
	WatchRename
)

type WatchEvent struct {
	Path string
	Op   WatchOp
}

type FileWatcher interface {
	Add(path string) error
	Close() error
	Events() <-chan WatchEvent
	Errors() <-chan error
}

type WatcherFactory func() (FileWatcher, error)

type fsnotifyFileWatcher struct {
	watcher *fsnotify.Watcher
	events  chan WatchEvent
	errors  chan error
}

const fsnotifyForwardBuffer = 1024

func NewFSNotifyWatcher() (FileWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	out := &fsnotifyFileWatcher{
		watcher: watcher,
		events:  make(chan WatchEvent, fsnotifyForwardBuffer),
		errors:  make(chan error, fsnotifyForwardBuffer),
	}
	go out.forward()
	return out, nil
}

func (watcher *fsnotifyFileWatcher) Add(path string) error {
	return watcher.watcher.Add(path)
}

func (watcher *fsnotifyFileWatcher) Close() error {
	return watcher.watcher.Close()
}

func (watcher *fsnotifyFileWatcher) Events() <-chan WatchEvent {
	return watcher.events
}

func (watcher *fsnotifyFileWatcher) Errors() <-chan error {
	return watcher.errors
}

func (watcher *fsnotifyFileWatcher) forward() {
	defer close(watcher.events)
	defer close(watcher.errors)
	for {
		select {
		case event, ok := <-watcher.watcher.Events:
			if !ok {
				return
			}
			watcher.events <- WatchEvent{Path: event.Name, Op: convertWatchOp(event.Op)}
		case err, ok := <-watcher.watcher.Errors:
			if !ok {
				return
			}
			watcher.errors <- err
		}
	}
}

func convertWatchOp(op fsnotify.Op) WatchOp {
	var converted WatchOp
	if op&fsnotify.Create != 0 {
		converted |= WatchCreate
	}
	if op&fsnotify.Write != 0 {
		converted |= WatchWrite
	}
	if op&fsnotify.Remove != 0 {
		converted |= WatchRemove
	}
	if op&fsnotify.Rename != 0 {
		converted |= WatchRename
	}
	return converted
}

func isWatcherOverflow(err error) bool {
	return errors.Is(err, fsnotify.ErrEventOverflow)
}

func isDirectoryPath(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.IsDir() && info.Mode()&os.ModeSymlink == 0
}
