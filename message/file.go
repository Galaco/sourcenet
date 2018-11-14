package message

type File struct {
	path string
	data []byte
}

// Connectionless: is this message a connectionless message?
func (msg *File) Connectionless() bool {
	return false
}

// Data: Get packet data
func (msg *File) Data() []byte {
	return msg.data
}

func NewFile(filepath string, data []byte) *File {
	return &File{
		path: filepath,
		data: data,
	}
}
