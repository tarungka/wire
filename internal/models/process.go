package models



type ProcessInfo struct{
	Name   string
	Ip     string
	Port   string
	Delays []int
}

type Message struct {
	Sender       string
	Receiver     string
	NetworkDelay int
	Body         int
}