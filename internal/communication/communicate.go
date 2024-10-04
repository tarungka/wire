package communication

import (
	"net"

	"github.com/tarungka/wire/internal/models"
)

type CommunicationModule struct {
	ProcessId         int
	NetworkInfo       []models.ProcessInfo
	listener          net.Listener
	processMessageOut chan models.Message
	processMessageIn  chan models.Message
	MarkMessageIn     chan models.Message
	MarkMessageOut    chan int
}

func Test(){}

// func CreateCommunicationModule(pid int, network []models.ProcessInfo, processMessageIn chan models.Message, processMessageOut chan models.Message, markMessageIn chan models.Message, markMessageOut chan int) *CommunicationModule {
// func CreateCommunicationModule() *CommunicationModule {
// 	process := network[pid]
// 	log.Debug().Msgf("Listening %v on TCP on port %v\n", pid, process)
// 	listener, err := net.Listen("tcp", ":"+process.Port)

// 	if err != nil {
// 		panic(fmt.Sprintf("Server listen error %v", err))
// 	}

// 	var communicationModule = CommunicationModule{
// 		ProcessId:         pid,
// 		NetworkInfo:       network,
// 		listener:          listener,
// 		processMessageIn:  processMessageIn,
// 		processMessageOut: processMessageOut,
// 		MarkMessageIn:     markMessageIn,
// 		MarkMessageOut:    markMessageOut,
// 	}

// 	// go communicationModule.receiver()
// 	// go communicationModule.sender()
// 	return &communicationModule
// }

// // This method should wait for messages from other processes
// func (comMod *CommunicationModule) receiver() {
// 	for {
// 		data := new(models.Message)
// 		err := helpers.Receive(data, &comMod.listener, comMod.Logger)
// 		if err != nil {
// 			panic(err)
// 		}
// 		time.Sleep(time.Duration(data.NetworkDelay) * time.Millisecond)
// 		switch data.MsgType {
// 		case models.MsgApp:
// 			comMod.processMessageIn <- *data
// 		case models.MsgMark:
// 			comMod.MarkMessageIn <- *data
// 		}
// 	}
// }

// // This method should send any message to other processes, not concurrent
// func (comMod *CommunicationModule) sender() {
// 	for {
// 		select {
// 		case processMsg := <-comMod.processMessageOut:
// 			for _, proc := range comMod.NetworkInfo {
// 				if proc.Name == processMsg.Receiver {
// 					helpers.Send(processMsg, proc.Ip+":"+proc.Port, comMod.Logger)
// 				}
// 			}

// 		case delay := <-comMod.MarkMessageOut: // send mark message to take snapshot
// 			for i, proc := range comMod.NetworkInfo {
// 				if i != comMod.ProcessId {
// 					totalDelay := delay + comMod.NetworkInfo[comMod.ProcessId].Delays[i]
// 					markMsg := models.NewMarkMessage(comMod.NetworkInfo[comMod.ProcessId].Name, comMod.NetworkInfo[i].Name, totalDelay)
// 					helpers.Send(markMsg, proc.Ip+":"+proc.Port, comMod.Logger)
// 				}
// 			}
// 		}
// 	}
// }
