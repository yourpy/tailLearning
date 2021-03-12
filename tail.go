package main

import (
	"bufio"
	"errors"
	"fmt"
	"gopkg.in/tomb.v1"
	"io"
	"os"
	"strings"
	"sync"
	"test/watch"
	"time"
)

var (
	ErrStop = errors.New("tail should now stop")
)

var errStopAtEOF = errors.New("tail: stop at eof")

// Line 结构体用于保存读取到的每行的对象
type Line struct {
	Text     string    // 当前行的内容
	//Num      int       // 行数
	//SeekInfo SeekInfo  // 开始读取数据的位置
	Time     time.Time // 读取数据的当前时间
	Err      error     // Error from tail
}

// SeekInfo represents arguments to `io.Seek`
// 查找的相关信息，标记从哪行开始读取
type SeekInfo struct {
	Offset int64 // 偏移量
	Whence int   // io.Seek*
}

// 关于配置的结构体
type Config struct {
	Location  *SeekInfo // 打开文件开始读取时光标的位置
	ReOpen    bool      // Reopen recreated files (tail -F)
	MustExist bool      // 要打开的文件是否必须存在
	Poll      bool

	Pipe bool

	Follow bool // 是否继续读取新的一行，可以理解为tail -f 命令

}

// 核心的结构体Tail
type Tail struct {
	Filename string     // 要打开的文件名
	Lines    chan *Line // 用于存每行内容的Line结构体

	Config

	watcher watch.FileWatcher
	changes *watch.FileChanges

	tomb.Tomb

	file   *os.File
	reader *bufio.Reader
	lk     sync.Mutex
}

// 用于 Tail 结构体的初始化
func TailFile(filename string, config Config) (*Tail, error) {
	t := &Tail{
		Filename: filename,
		Lines:    make(chan *Line),
		Config:   config,
	}
	t.watcher = watch.NewInotifyFileWatcher(filename)
	if t.MustExist {
		var err error
		t.file, err = OpenFile(t.Filename)
		if err != nil {
			return nil, err
		}
	}
	go t.tailFileSync()

	return t, nil
}

// 获得文件当前行的位置信息
func (tail *Tail) Tell() (offset int64, err error) {
	// 文件不存在，直接返回
	if tail.file == nil {
		return
	}
	offset, err = tail.file.Seek(0, os.SEEK_CUR)
	if err != nil {
		return
	}
	tail.lk.Lock()
	defer tail.lk.Unlock()
	if tail.reader == nil {
		return
	}
	offset -= int64(tail.reader.Buffered())
	return
}

func (tail *Tail)tailFileSync() {
	defer tail.Done()
	defer tail.close()

	if !tail.MustExist {
		err := tail.reopen()
		if err != nil {
			tail.Kill(err)
			return
		}
	}

	tail.openReader()

	var offset int64
	var err error

	// 一行一行读取文件内容
	for {
		if !tail.Pipe {
			offset, err = tail.Tell()
			if err != nil {
				tail.Kill(err)
				return
			}
		}

		line, err := tail.readLine()
		if err == nil {
			// 将读取到的一行内容放到chan中
			tail.sendLine(line)
		} else if err == io.EOF {
			// io.EOF 表示读取到文件的最后了
			// 如果 Follow 设置为 false 就不会继续读取文件了
			if !tail.Follow {
				if line != "" {
					tail.sendLine(line)
				}
				return
			}
			// 如果 Follow 设置为 true 就会继续读取
			if tail.Follow && line != "" {
				err := tail.seekTo(SeekInfo{
					Offset: offset,
					Whence: 0,
				})
				if err != nil {
					tail.Kill(err)
					return
				}
			}
			// 如果读取到文件最后，文件没有新的内容增加
			err := tail.waitForChanges()
			if err != nil {
				if err != ErrStop {
					tail.Kill(err)
				}
				return
			}
		} else {
			// 既不是文件结尾，也没有error
			tail.Killf("error reading %s :%s", tail.Filename, err)
			return
		}

		select {
		case <-tail.Dying():
			if tail.Err() == errStopAtEOF {
				continue
			}
			return
		default:
		}
	}
}

// 等待文件的变化事件
func (tail *Tail) waitForChanges() error {
	if tail.changes == nil {
		// 获取文件指针当前的位置
		pos, err := tail.file.Seek(0, os.SEEK_CUR)
		if err != nil {
			return err
		}
		tail.changes, err = tail.watcher.ChangeEvents(&tail.Tomb, pos)
		if err != nil {
			return err
		}
	}

	// 和 inotify 进行很巧妙的配合，这里通过 select 来查看哪个 chan 变化了
	select {
	case <- tail.changes.Modified: // 文件被修改
		return nil
	case <-tail.changes.Deleted: // 文件被删除或者移动
		tail.changes = nil
		// 如果文件被删除或者移动到其他目录，则会尝试重新打开文件
		if tail.ReOpen {
			fmt.Printf("Re-opening moved/deleted file %s...",tail.Filename)
			if err := tail.reopen(); err != nil {
				return err
			}
			fmt.Printf("Successfully reopened %s", tail.Filename)
			tail.openReader()
			return nil
		} else {
			fmt.Printf("Stoping tail as file not longer exists: %s", tail.Filename)
			return ErrStop
		}
	case <- tail.changes.Truncated: // 文件被追加新的内容
		fmt.Printf("Re-opening truncated file %s....", tail.Filename)
		if err := tail.reopen();err != nil {
			return err
		}
		fmt.Printf("SuccessFuly reopend truncated %s", tail.Filename)
		tail.openReader()
		return nil
	case <- tail.Dying():
		return nil
	}
	panic("unreachable")
}

func (tail *Tail) openReader() {
	tail.reader = bufio.NewReader(tail.file)
}

// 读文件的每一行内容
func (tail *Tail) readLine() (string, error) {
	tail.lk.Lock()
	line, err := tail.reader.ReadString('\n')
	defer tail.lk.Unlock()

	if err != nil {
		return line, err
	}
	line = strings.TrimRight(line, "\n")
	return line, err
}

// 将读取的文件的每行内容存入到 Line 结构体中，并最终存入到 tail.Lines 的 chan 中
func (tail *Tail) sendLine(line string) bool {
	now := time.Now()
	lines := []string{line}

	for _, line := range lines {
		tail.Lines <- &Line{
			line,
			now,
			nil,
		}
	}
	return true
}

// 将光标移动到 pos 给定的位置
func (tail *Tail) seekTo(pos SeekInfo) error {
	_, err := tail.file.Seek(pos.Offset, pos.Whence)
	if err != nil {
		return fmt.Errorf("seek error on %s: %s", tail.Filename, err)
	}
	tail.reader.Reset(tail.file)
	return nil
}

// 移动到文件尾部
func (tail *Tail) seekEnd() error {
	return tail.seekTo(SeekInfo{
		Offset: 0,
		Whence: os.SEEK_CUR,
	})
}

func (tail *Tail) closeFile() {
	if tail.file != nil {
		tail.file.Close()
		tail.file = nil
	}
}

func (tail *Tail) reopen() error {
	tail.closeFile()
	for {
		var err error
		tail.file, err = OpenFile(tail.Filename)
		if err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("Waiting for %s to appear....", tail.Filename)
				if err := tail.watcher.BlockUntilExists(&tail.Tomb);err != nil {
					if err  == tomb.ErrDying {
						return err
					}
					return fmt.Errorf("Failed to detect creation of %s:%s", tail.Filename, err)
				}
				continue
			}
			return fmt.Errorf("Unable to open file %s:%s", tail.Filename, err)
		}
		break
	}
	return nil
}

func (tail *Tail) Cleanup() {
	watch.Cleanup(tail.Filename)
}

func OpenFile(name string) (file *os.File, err error) {
	return os.Open(name)
}
