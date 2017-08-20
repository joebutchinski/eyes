package controller

import (
	"net"
	"sync"

	"github.com/golang/protobuf/proto"
	"github.com/rs/zerolog/log"
	"github.com/sh3rp/eyes/messages"
	"github.com/sh3rp/eyes/util"
)

var V_MAJOR = 0
var V_MINOR = 1
var V_PATCH = 0

type ProbeController struct {
	Agents             map[string]*ProbeAgent
	ResultChannel      chan *messages.AgentProbeResult
	ResultListeners    []func(*messages.AgentProbeResult)
	DisconnectChannel  chan string
	agentLock          *sync.Mutex
	probeCallbacks     map[string]func(*messages.AgentProbeResult)
	probeCallbacksLock *sync.Mutex
}

func NewProbeController() *ProbeController {
	return &ProbeController{
		Agents:             make(map[string]*ProbeAgent),
		ResultChannel:      make(chan *messages.AgentProbeResult, 10),
		DisconnectChannel:  make(chan string, 5),
		agentLock:          new(sync.Mutex),
		probeCallbacks:     make(map[string]func(*messages.AgentProbeResult)),
		probeCallbacksLock: new(sync.Mutex),
	}
}

func (c *ProbeController) GetVersion() (int, int, int) {
	return V_MAJOR, V_MINOR, V_PATCH
}

func (c *ProbeController) AddResultListener(f func(*messages.AgentProbeResult)) {
	c.ResultListeners = append(c.ResultListeners, f)
}

func (c *ProbeController) ResultReadLoop() {
	log.Info().Msgf("ResultReadLoop: starting")
	for {
		result := <-c.ResultChannel
		for _, listener := range c.ResultListeners {
			listener(result)
		}
		c.probeCallbacksLock.Lock()
		if _, ok := c.probeCallbacks[result.ResultId]; ok {
			c.probeCallbacks[result.ResultId](result)
		}
		delete(c.probeCallbacks, result.ResultId)
		c.probeCallbacksLock.Unlock()
	}
}

func (c *ProbeController) DisconnectHandler() {
	log.Info().Msgf("DisconnectHandler: starting")
	for {
		disconnect := <-c.DisconnectChannel
		c.agentLock.Lock()
		if _, ok := c.Agents[disconnect]; ok {
			delete(c.Agents, disconnect)
		}
		c.agentLock.Unlock()
	}
}

func (c *ProbeController) Start() {
	log.Info().Msgf("Controller: starting")

	ln, err := net.Listen("tcp", ":12121")

	if err != nil {
		log.Error().Msgf("Error listening on socket: %v", err)
		return
	}

	go c.ResultReadLoop()
	go c.DisconnectHandler()

	for {
		conn, err := ln.Accept()

		if err != nil {
			log.Error().Msgf("Error accepting connection: %v", err)
		} else {
			go c.handle(conn)
		}
	}
}

func (c *ProbeController) SendProbe(agentId string, latencyRequest *messages.LatencyRequest) string {
	if latencyRequest.ResultId == "" {
		latencyRequest.ResultId = util.GenID()
	}
	c.agentLock.Lock()
	defer c.agentLock.Unlock()
	if v, ok := c.Agents[agentId]; ok {
		v.SendCommand(&messages.ControllerMessage{
			Type:           messages.ControllerMessage_LATENCY_REQUEST,
			LatencyRequest: latencyRequest,
		})
	} else {
		log.Error().Msgf("SendProbe failed, no such agentId %s", agentId)
	}
	return latencyRequest.ResultId
}

func (c *ProbeController) SendProbeCallback(agentId string, latencyRequest *messages.LatencyRequest, f func(*messages.AgentProbeResult)) {
	id := util.GenID()
	c.probeCallbacksLock.Lock()
	c.probeCallbacks[id] = f
	c.probeCallbacksLock.Unlock()
	latencyRequest.ResultId = id
	c.SendProbe(agentId, latencyRequest)
}

func (c *ProbeController) handle(conn net.Conn) {
	data := make([]byte, 4096)
	len, err := conn.Read(data)

	if err != nil {
		log.Error().Msgf("ERROR handle (read): %v", err)
		return
	}

	agentMessage := &messages.AgentMessage{}
	err = proto.Unmarshal(data[:len], agentMessage)

	if err != nil {
		log.Error().Msgf("ERROR handle (marshal): %v", err)
		return
	}

	c.agentLock.Lock()

	c.Agents[agentMessage.Id] = &ProbeAgent{
		Id:         agentMessage.Id,
		Info:       agentMessage.Info,
		Connection: conn,
	}

	c.agentLock.Unlock()

	go c.Agents[agentMessage.Id].ReadLoop(c.ResultChannel, c.DisconnectChannel)

	log.Info().Msgf("Agent connected: %s (%v) - (%v)", agentMessage.Id, c.Agents[agentMessage.Id].Info.Ipaddress, c.Agents[agentMessage.Id].Info.Label)
}
