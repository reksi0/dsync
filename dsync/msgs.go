package main

const (
	FileUpdated = 1
	FileRemoved = 2

	FileNewData   = 3
	FileReuseData = 4
	FileEnd       = 5
)

type ClientMsg struct {
	File   string
	Op     int
	SeqNo  int
	Length int
	Data   []byte
}

type ServerMsg struct {
	File   string
	Last   bool
	SeqNo  int
	Length int
	Hash   [16]byte
}
