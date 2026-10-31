package camera

import "encoding/binary"

var le = binary.LittleEndian

type Message struct {
	Sender, Receiver byte
	Seq              uint16
	Flags, Set, ID   byte
	Payload          []byte
}

func CRC8(data []byte) byte {
	c := byte(0x77)
	for _, b := range data {
		c ^= b
		for range 8 {
			if c&1 != 0 {
				c = c>>1 ^ 0x8c
			} else {
				c >>= 1
			}
		}
	}
	return c
}

func CRC16(data []byte) uint16 {
	c := uint16(0x3692)
	for _, b := range data {
		c ^= uint16(b)
		for range 8 {
			if c&1 != 0 {
				c = c>>1 ^ 0x8408
			} else {
				c >>= 1
			}
		}
	}
	return c
}

func (m Message) Encode() []byte {
	if len(m.Payload) > 1010 {
		return nil
	}
	b := make([]byte, 13+len(m.Payload))
	b[0] = 0x55
	le.PutUint16(b[1:], uint16(len(b))|0x400)
	b[3] = CRC8(b[:3])
	b[4] = m.Sender
	b[5] = m.Receiver
	le.PutUint16(b[6:], m.Seq)
	b[8] = m.Flags
	b[9] = m.Set
	b[10] = m.ID
	copy(b[11:], m.Payload)
	le.PutUint16(b[len(b)-2:], CRC16(b[:len(b)-2]))
	return b
}

type Reader struct{ pending []byte }

func (r *Reader) Feed(data []byte) []Message {
	r.pending = append(r.pending, data...)
	var out []Message
	for len(r.pending) >= 4 {
		b := r.pending
		if b[0] != 0x55 || CRC8(b[:3]) != b[3] {
			r.pending = b[1:]
			continue
		}
		n := int(le.Uint16(b[1:]) & 0x3ff)
		if n < 13 {
			r.pending = b[1:]
			continue
		}
		if len(b) < n {
			break
		}
		r.pending = b[n:]
		if CRC16(b[:n-2]) != le.Uint16(b[n-2:]) {
			continue
		}
		out = append(out, Message{b[4], b[5], le.Uint16(b[6:]), b[8], b[9], b[10], append([]byte(nil), b[11:n-2]...)})
	}
	return out
}

func Pairing(identity string) Message {
	return pairingWithToken(identity, "osmo")
}

// AircraftPairing 使用 DJI Fly 的飞行器配对标识；相机标识虽能授权，却不能完成无人机快传。
func AircraftPairing(identity string) Message {
	return pairingWithToken(identity, "DJI FLY")
}

func pairingWithToken(identity, token string) Message {
	p := append([]byte{byte(len(identity))}, []byte(identity)...)
	p = append(p, byte(len(token)))
	p = append(p, token...)
	return Message{2, 7, 0x8092, 0x40, 7, 0x45, p}
}

func appInfo() []byte {
	p := make([]byte, 62)
	copy(p, []byte{0, 'A', 'P', 'P'})
	p[41] = 2
	p[50] = 2
	p[51] = 8
	return p
}

func Packet(kind byte, session, seq uint16, body []byte) []byte {
	b := make([]byte, 8+len(body))
	le.PutUint16(b, uint16(len(b))|0x8000)
	le.PutUint16(b[2:], session)
	le.PutUint16(b[4:], seq)
	b[6] = kind
	for _, v := range b[:7] {
		b[7] ^= v
	}
	copy(b[8:], body)
	return b
}

func validPacket(b []byte, session uint16) bool {
	if len(b) < 8 || int(le.Uint16(b)&0x3fff) != len(b) || le.Uint16(b[2:]) != session {
		return false
	}
	var xor byte
	for _, v := range b[:8] {
		xor ^= v
	}
	return xor == 0
}
