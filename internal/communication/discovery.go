package communication

import (
	"fmt"
	"net"

	"github.com/DistributedClocks/GoVector/govec"
)

func DiscoverPeers(advertisedListeners []string) {
	for index, listener := range advertisedListeners {
		fmt.Printf("%v -- %v\n", index, listener)
		conn, err := net.Dial("tcp", listener)

		if err != nil {
			fmt.Printf("Error is: %v\n", err)
			panic("Client connection error")
		}

		processId := "1"
		defaultConfig := govec.GetDefaultConfig()
		defaultConfig.UseTimestamps = true
		goVector := govec.InitGoVector(processId, "govector/"+processId, defaultConfig)

		binBuffer := goVector.PrepareSend("Send", "", govec.GetDefaultLogOptions())

		conn.Write(binBuffer)
		defer conn.Close()
	}

}
