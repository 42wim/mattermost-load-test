// Copyright (c) 2016 Mattermost, Inc. All Rights Reserved.
// See License.txt for license information.

package main

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/mattermost/mattermost-load-test/cmd/cmdlib"
	"github.com/mattermost/mattermost-load-test/loadtestconfig"
	"github.com/mattermost/platform/model"
)

type UserEntityLogger interface {
	Println(a ...interface{}) (int, error)
}

type TmpLogger struct {
	Writer io.Writer
}

func (logger *TmpLogger) Println(a ...interface{}) (int, error) {
	return fmt.Fprintln(logger.Writer, a...)
}

func StartUserEntities(config *loadtestconfig.LoadTestConfig, serverState *loadtestconfig.ServerState, entityCreationFunctions ...UserEntityCreator) {
	// Create a channel to signal a stop command
	stopChan := make(chan bool)

	// Create a wait group so we can wait for our entites to complete
	var stopWait sync.WaitGroup

	// Create Channel for users to report status
	statusPrinterStopChan := make(chan bool)

	// Waitgroup for waiting for status messages to finish printing
	var printerWait sync.WaitGroup

	// Channel to recieve user entity status reports
	statusChannel := make(chan UserEntityStatusReport, 1000)

	// Wait group so all entities start staggered properly
	var entityWaitChannel sync.WaitGroup
	entityWaitChannel.Add(1)

	// Create writer
	out := &TmpLogger{
		Writer: os.Stdout,
	}

	printerWait.Add(1)
	go UserEntityStatusPrinter(out, statusChannel, statusPrinterStopChan, &printerWait, serverState.Users)

	numEntities := config.UserEntitiesConfiguration.NumClientEntities

	out.Println("------------------------- Starting " + strconv.Itoa(numEntities) + " entities")
	for entityNum := 0; entityNum < numEntities; entityNum++ {
		out.Println("Starting Entity: " + strconv.Itoa(entityNum))
		// Get the user for this entity. If there are not enough users
		// for the number of entities requested, wrap around.
		entityUser := &serverState.Users[entityNum%len(serverState.Users)]

		userClient := cmdlib.GetUserClient(&config.ConnectionConfiguration, entityUser)

		websocketURL := config.ConnectionConfiguration.WebsocketURL
		userWebsocketClient := &model.WebSocketClient{
			websocketURL,
			websocketURL + model.API_URL_SUFFIX,
			nil,
			userClient.AuthToken,
			1,
			make(chan *model.WebSocketEvent, 100),
			make(chan *model.WebSocketResponse, 100),
			nil,
		}

		entityConfig := UserEntityConfig{
			Id:                  entityNum,
			EntityUser:          entityUser,
			Client:              userClient,
			WebSocketClient:     userWebsocketClient,
			LoadTestConfig:      config,
			State:               serverState,
			StatusReportChannel: statusChannel,
			StopEntityChannel:   stopChan,
			StopEntityWaitGroup: &stopWait,
		}
		stopWait.Add(len(entityCreationFunctions))
		for _, createEntity := range entityCreationFunctions {
			entity := createEntity(entityConfig)
			go func(entityNum int) {
				entityWaitChannel.Wait()
				time.Sleep(time.Duration(config.UserEntitiesConfiguration.EntityRampupDistanceMilliseconds) * time.Millisecond * time.Duration(entityNum))
				entity.Start()
			}(entityNum)
		}
	}
	// Release the entities
	entityWaitChannel.Done()

	out.Println("------------------------- Done starting entities")
	interrupChannel := make(chan os.Signal)
	signal.Notify(interrupChannel, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	<-interrupChannel
	out.Println("Shutdown signal recieved.")
	close(stopChan)

	out.Println("Waiting for user entities")
	stopWait.Wait()
	out.Println("Flushing status reporting channel")
	close(statusPrinterStopChan)
	printerWait.Wait()
	out.Println("DONE!")
}
