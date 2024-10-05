package communication

import (
	"fmt"
	"net"
	"time"

	"github.com/DistributedClocks/GoVector/govec"
	"github.com/rs/zerolog/log"
)

func DiscoverPeers(advertisedListeners []string) {
	for index, listener := range advertisedListeners {

		var conn net.Conn
		var err error

		fmt.Printf("%v -- %v\n", index, listener)
		for {
			conn, err = net.Dial("tcp", listener)
			if err != nil {
				waitTime := 2 // Wait time in seconds
				log.Info().Msgf("Error when discovering peer servers. Retrying in %d seconds...", waitTime)
				time.Sleep(time.Duration(waitTime) * time.Second)
				continue
			}

			break
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
