// 定一个了一个FileWatcher接口，需要实现两个方法：
// BlockUntilExists 和 ChangeEvents
package watch

import "gopkg.in/tomb.v1"


type FileWatcher interface {
	BlockUntilExists(*tomb.Tomb) error

	ChangeEvents(*tomb.Tomb, int64) (*FileChanges, error)
}
