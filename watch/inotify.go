/*
实现了FileWatcher 接口
*/

package watch

import (
	"fmt"
	"os"
	"gopkg.in/tomb.v1"
	"path/filepath"
	"gopkg.in/fsnotify.v1"
)


type InotifyFileWatcher struct {
	Filename 	string
	Size		int64
}

func NewInotifyFileWatcher(filename string) *InotifyFileWatcher {
	fw := &InotifyFileWatcher {
		filepath.Clean(filename),
		0,
	}
	return fw
}


// 关于文件改变事件的处理，当文件被修改了或者文件内容被追加了，进行通知
func (fw *InotifyFileWatcher) ChangeEvents(t *tomb.Tomb, pos int64) (*FileChanges, error) {
	err := Watch(fw.Filename)
	if err != nil {
		return nil, err
	}

	changes := NewFileChanges()
	fw.Size = pos

	go func() {
		events := Events(fw.Filename)

		for {
			prevSize := fw.Size

			var evt fsnotify.Event
			var ok bool
			
			select {
			case evt, ok = <- events:
				if !ok {
					RemoveWatch(fw.Filename)
					return
				}
			case <- t.Dying():
				RemoveWatch(fw.Filename)
				return
			}
			switch {
			case evt.Op & fsnotify.Remove == fsnotify.Remove:
				fallthrough
			case evt.Op & fsnotify.Rename == fsnotify.Rename:
				RemoveWatch(fw.Filename)
				changes.NotifyDeleted()
				return

			case evt.Op & fsnotify.Chmod == fsnotify.Chmod:
				fallthrough
			case evt.Op & fsnotify.Write == fsnotify.Write:
				fi, err := os.Stat(fw.Filename)
				if err != nil {
					// 文件如果被删除了通知文件删除到chan
					if os.IsNotExist(err) {
						RemoveWatch(fw.Filename)
						changes.NotifyDeleted()
						return
					}

				}
				fw.Size = fi.Size()

				if prevSize > 0 && prevSize > fw.Size {
					// 表示文件内容增加了
					changes.NotifyTruncated()
				} else {
					// 表示文件被修改了
					changes.NotifyModified()
				}

				prevSize = fw.Size
			}

		}
	}()
	return changes, nil
}

func (fw *InotifyFileWatcher) BlockUntilExists(t *tomb.Tomb) error {
	err := WatchCreate(fw.Filename)
	if err != nil {
		return err
	}
	defer RemoveWatchCreate(fw.Filename)
	if _, err := os.Stat(fw.Filename);!os.IsNotExist(err) {
		return err
	}
	events := Events(fw.Filename)
	for {
		select {
		case evt, ok := <- events:
			if !ok {
				return fmt.Errorf("inotify watcher has been closed")
			}
			evtName, err := filepath.Abs(evt.Name)
			if err != nil {
				return err
			}
			fwFilename, err := filepath.Abs(fw.Filename)
			if err != nil {
				return err
			}
			if evtName == fwFilename {
				return nil
			}
		case <- t.Dying():
			return tomb.ErrDying
		}
	}
	panic("unreachable")
}
