package watch

type FileChanges struct {
	Modified  chan bool		// 修改
	Truncated chan bool		// 增加
	Deleted   chan bool		// 删除
}

func NewFileChanges() *FileChanges {
	return &FileChanges{
		make(chan bool, 1),
		make(chan bool, 1),
		make(chan bool, 1),
	}
}

func (fc *FileChanges) NotifyModified() {
	sendOnlyIfEmpty(fc.Modified)
}

func (fc *FileChanges) NotifyTruncated() {
	sendOnlyIfEmpty(fc.Truncated)
}

func (fc *FileChanges) NotifyDeleted() {
	sendOnlyIfEmpty(fc.Deleted)
}

func sendOnlyIfEmpty(ch chan bool) {
	select {
	case ch <- true:
	default:
	}
}
