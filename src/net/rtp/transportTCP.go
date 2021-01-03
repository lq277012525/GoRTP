package rtp

import (
	"fmt"
	"io"
	"net"
)

type TCPPackerizerDataType int

const (
	TCPPackerizerRTPData TCPPackerizerDataType = iota
	TCPPackerizerRTCPData
	TCPPackerizerRTSPData
	TCPPackerizerAPPData
)

type TransportTCPPackerizer interface {
	Pack([]byte, TCPPackerizerDataType) []byte
	UnPack(reader io.Reader) ([]byte, TCPPackerizerDataType, error)
}

// RtpTransportUDP implements the interfaces RtpTransportRecv and RtpTransportWrite for RTP transports.
type TransportTCP struct {
	TransportCommon
	callUpper  TransportRecv
	toLower    TransportWrite
	dataConn   *net.TCPConn
	localAddr  net.TCPAddr
	remoteAddr net.TCPAddr
	packerizer TransportTCPPackerizer
}

// NewRtpTransportUDP creates a new RTP transport for UPD.
//
// addr - The UPD socket's local IP address
//
// port - The port number of the RTP data port. This must be an even port number.
//        The following odd port number is the control (RTCP) port.
//
func NewTransportTCP(con *net.TCPConn, packer TransportTCPPackerizer) (*TransportTCP, error) {
	tp := new(TransportTCP)
	tp.callUpper = tp
	tp.toLower = tp
	tp.dataConn = con
	tp.localAddr = *con.LocalAddr().(*net.TCPAddr)
	tp.remoteAddr = *con.RemoteAddr().(*net.TCPAddr)
	tp.packerizer = packer
	return tp, nil
}

// ListenOnTransports listens for incoming RTP and RTCP packets addressed
// to this transport.
//
func (tp *TransportTCP) ListenOnTransports() (err error) {
	go tp.readDataPacket()
	return nil
}

// *** The following methods implement the rtp.TransportRecv interface.

// SetCallUpper implements the rtp.TransportRecv SetCallUpper method.
func (tp *TransportTCP) SetCallUpper(upper TransportRecv) {
	tp.callUpper = upper
}

// OnRecvRtp implements the rtp.TransportRecv OnRecvRtp method.
//
// TransportUDP does not implement any processing because it is the lowest
// layer and expects an upper layer to receive data.
func (tp *TransportTCP) OnRecvData(rp *DataPacket) bool {
	fmt.Printf("TransportUDP: no registered upper layer RTP packet handler\n")
	return false
}
func (tp *TransportTCP) OnRecvRaw(rp *RawPacket) bool {
	fmt.Printf("TransportUDP: no registered upper layer raw packet handler\n")
	return false
}

// OnRecvRtcp implements the rtp.TransportRecv OnRecvRtcp method.
//
// TransportUDP does not implement any processing because it is the lowest
// layer and expects an upper layer to receive data.
func (tp *TransportTCP) OnRecvCtrl(rp *CtrlPacket) bool {
	fmt.Printf("TransportUDP: no registered upper layer RTCP packet handler\n")
	return false
}

// CloseRecv implements the rtp.TransportRecv CloseRecv method.
func (tp *TransportTCP) CloseRecv() {
	//
	// The correct way to do it is to close the UDP connection after setting the
	// stop flags to true. However, until issue 2116 is solved just set the flags
	// and rely on the read timeout in the read packet functions
	//
	tp.dataRecvStop = true
	tp.ctrlRecvStop = true

	err := tp.dataConn.Close()
	if err != nil {
		fmt.Printf("Close failed: %s\n", err.Error())
	}
}

// setEndChannel receives and set the channel to signal back after network socket was closed and receive loop terminated.
func (tp *TransportTCP) SetEndChannel(ch TransportEnd) {
	tp.transportEnd = ch
}

// *** The following methods implement the rtp.TransportWrite interface.

// SetToLower implements the rtp.TransportWrite SetToLower method.
//
// Usually TransportUDP is already the lowest layer.
func (tp *TransportTCP) SetToLower(lower TransportWrite) {
	tp.toLower = lower
}

// WriteRtpTo implements the rtp.TransportWrite WriteRtpTo method.
func (tp *TransportTCP) WriteDataTo(rp *DataPacket, addr *Address) (n int, err error) {
	return tp.dataConn.Write(tp.packerizer.Pack(rp.buffer[0:rp.inUse], TCPPackerizerRTPData))
}

// WriteRtcpTo implements the rtp.TransportWrite WriteRtcpTo method.
func (tp *TransportTCP) WriteCtrlTo(rp *CtrlPacket, addr *Address) (n int, err error) {
	return tp.dataConn.Write(tp.packerizer.Pack(rp.buffer[0:rp.inUse], TCPPackerizerRTCPData))
}
func (tp *TransportTCP) WriteRawTo(rp *RawPacket, addr *Address) (n int, err error) {
	return tp.dataConn.Write(rp.buffer[0:rp.inUse])
}

// CloseWrite implements the rtp.TransportWrite CloseWrite method.
//
// Nothing to do for TransportUDP. The application shall close the receiver (CloseRecv()), this will
// close the local UDP socket.
func (tp *TransportTCP) CloseWrite() {
}

// *** Local functions and methods.

// Here the local RTP and RTCP UDP network receivers. The ListenOnTransports() starts them
// as go functions. The functions just receive data from the network, copy it into
// the packet buffers and forward the packets to the next upper layer via callback
// if callback is not nil

func (tp *TransportTCP) readDataPacket() {
	//var buf [1500]byte

	tp.dataRecvStop = false
	for {
		// deadLineErr := tp.dataConn.SetReadDeadline(time.Now().Add(20 * time.Millisecond)) // 20 ms, re-test and remove after Go issue 2116 is solved
		data, ty, err := tp.packerizer.UnPack(tp.dataConn)
		if err != nil {
			break
		}
		if tp.ctrlRecvStop && tp.dataRecvStop {
			break
		}
		switch ty {
		case TCPPackerizerRTPData:
			if !tp.dataRecvStop {
				rp := newDataPacket()
				rp.fromAddr.IPAddr = tp.remoteAddr.IP
				rp.fromAddr.DataPort = tp.remoteAddr.Port
				rp.fromAddr.CtrlPort = 0
				rp.Append(data)
				if tp.callUpper != nil {
					tp.callUpper.OnRecvData(rp)
				}
			}
		case TCPPackerizerRTCPData:
			if !tp.ctrlRecvStop {
				rp, _ := newCtrlPacket()
				rp.fromAddr.IPAddr = tp.remoteAddr.IP
				rp.fromAddr.DataPort = 0
				rp.fromAddr.CtrlPort = tp.remoteAddr.Port
				rp.Append(data)
				if tp.callUpper != nil {
					tp.callUpper.OnRecvCtrl(rp)
				}
			}
		case TCPPackerizerAPPData, TCPPackerizerRTSPData:
			rp := &RawPacket{}
			rp.fromAddr.IPAddr = tp.remoteAddr.IP
			rp.fromAddr.DataPort = tp.remoteAddr.Port
			rp.fromAddr.CtrlPort = int(ty)
			rp.Append(data)
			if tp.callUpper != nil {
				tp.callUpper.OnRecvRaw(rp)
			}
		}
	}
	tp.dataConn.Close()
	tp.transportEnd <- DataTransportRecvStopped
}
