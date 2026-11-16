//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"syscall"
	"time"
)

// A private route protocol + metric and the exact saved tuple are required for
// deletion. No table flushes, shell loops or per-prefix fsync are used.
// Linux UAPI NLM_F_DUMP_INTR is absent from Go's frozen syscall package.
const netlinkDumpInterrupted = 0x10

type routeSocket struct {
	fd       int
	sequence uint32
}

func (socket *routeSocket) Close() error {
	return syscall.Close(socket.fd)
}

func openRouteSocket() (*routeSocket, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_RAW|syscall.SOCK_CLOEXEC, syscall.NETLINK_ROUTE)
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*routeSocket, error) { return nil, errors.Join(err, syscall.Close(fd)) }
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return fail(err)
	}
	timeout := syscall.NsecToTimeval((250 * time.Millisecond).Nanoseconds())
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &timeout); err != nil {
		return fail(err)
	}
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_SNDTIMEO, &timeout); err != nil {
		return fail(err)
	}
	return &routeSocket{fd: fd}, nil
}

func (socket *routeSocket) packet(kind, flags uint16, body []byte) ([]byte, uint32) {
	socket.sequence++
	packet := make([]byte, 16+len(body))
	binary.NativeEndian.PutUint32(packet, uint32(len(packet)))
	binary.NativeEndian.PutUint16(packet[4:], kind)
	binary.NativeEndian.PutUint16(packet[6:], flags)
	binary.NativeEndian.PutUint32(packet[8:], socket.sequence)
	copy(packet[16:], body)
	return packet, socket.sequence
}

func (socket *routeSocket) receive(ctx context.Context) ([]syscall.NetlinkMessage, error) {
	deadline := time.Now().Add(5 * time.Second)
	buffer := make([]byte, 65536)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		count, from, err := syscall.Recvfrom(socket.fd, buffer, 0)
		if err == syscall.EINTR || err == syscall.EAGAIN {
			continue
		}
		if err != nil {
			return nil, err
		}
		address, ok := from.(*syscall.SockaddrNetlink)
		if !ok || address.Pid != 0 {
			return nil, errors.New("unexpected route socket sender")
		}
		return syscall.ParseNetlinkMessage(buffer[:count])
	}
	return nil, errors.New("route socket response timed out")
}

func kernelManagedRoutes(ctx context.Context) ([]managedRoute, error) {
	socket, err := openRouteSocket()
	if err != nil {
		return nil, err
	}
	defer closeResource(socket)
	body := make([]byte, 12)
	body[0] = syscall.AF_INET
	request, sequence := socket.packet(syscall.RTM_GETROUTE, syscall.NLM_F_REQUEST|syscall.NLM_F_DUMP, body)
	if err := syscall.Sendto(socket.fd, request, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
		return nil, err
	}
	var result []managedRoute
	total := 0
	for packets := 0; packets < 1024; packets++ {
		messages, err := socket.receive(ctx)
		if err != nil {
			return nil, err
		}
		for _, message := range messages {
			if message.Header.Seq != sequence {
				continue
			}
			if message.Header.Flags&netlinkDumpInterrupted != 0 {
				return nil, errors.New("kernel route dump was interrupted; retry")
			}
			if message.Header.Type == syscall.NLMSG_DONE {
				return result, nil
			}
			if message.Header.Type == syscall.NLMSG_ERROR {
				return nil, errors.New("kernel route dump failed")
			}
			total += len(message.Data)
			if total > 4*1024*1024 {
				return nil, errors.New("kernel route dump exceeds limit")
			}
			if message.Header.Type != syscall.RTM_NEWROUTE || len(message.Data) < 12 {
				continue
			}
			data := message.Data
			if data[0] != syscall.AF_INET || data[3] != 0 || data[5] != 242 || data[7] != syscall.RTN_UNICAST {
				continue
			}
			attrs, err := syscall.ParseNetlinkRouteAttr(&message)
			if err != nil {
				return nil, err
			}
			table, metric, index := uint32(data[4]), uint32(0), uint32(0)
			address := netip.AddrFrom4([4]byte{})
			var gateway netip.Addr
			for _, attr := range attrs {
				if len(attr.Value) < 4 {
					continue
				}
				switch attr.Attr.Type {
				case syscall.RTA_TABLE:
					table = binary.NativeEndian.Uint32(attr.Value)
				case syscall.RTA_PRIORITY:
					metric = binary.NativeEndian.Uint32(attr.Value)
				case syscall.RTA_OIF:
					index = binary.NativeEndian.Uint32(attr.Value)
				case syscall.RTA_DST:
					address = netip.AddrFrom4([4]byte(attr.Value[:4]))
				case syscall.RTA_GATEWAY:
					gateway = netip.AddrFrom4([4]byte(attr.Value[:4]))
				}
			}
			if table != syscall.RT_TABLE_MAIN || metric != splitRouteMetric || index == 0 || data[1] > 32 {
				continue
			}
			result = append(result, managedRoute{netip.PrefixFrom(address, int(data[1])).Masked(), int(index), gateway})
		}
	}
	return nil, errors.New("kernel route dump did not terminate")
}

func routeAttribute(kind uint16, value []byte) []byte {
	size := 4 + len(value)
	result := make([]byte, (size+3)&^3)
	binary.NativeEndian.PutUint16(result, uint16(size))
	binary.NativeEndian.PutUint16(result[2:], kind)
	copy(result[4:], value)
	return result
}

func routeUintAttribute(kind uint16, value uint32) []byte {
	raw := make([]byte, 4)
	binary.NativeEndian.PutUint32(raw, value)
	return routeAttribute(kind, raw)
}

func routeBody(route managedRoute) []byte {
	body := make([]byte, 12)
	body[0] = syscall.AF_INET
	body[1] = byte(route.Prefix.Bits())
	body[4] = syscall.RT_TABLE_MAIN
	body[5] = 242
	body[7] = syscall.RTN_UNICAST
	if !route.Gateway.IsValid() {
		body[6] = syscall.RT_SCOPE_LINK
	}
	address := route.Prefix.Addr().As4()
	body = append(body, routeAttribute(syscall.RTA_DST, address[:])...)
	body = append(body, routeUintAttribute(syscall.RTA_OIF, uint32(route.Interface))...)
	body = append(body, routeUintAttribute(syscall.RTA_PRIORITY, splitRouteMetric)...)
	if route.Gateway.IsValid() {
		gateway := route.Gateway.As4()
		body = append(body, routeAttribute(syscall.RTA_GATEWAY, gateway[:])...)
	}
	return body
}

type routeMutation struct {
	Kind  uint16
	Flags uint16
	Route managedRoute
}

func mutateManagedRoutes(ctx context.Context, add, remove []managedRoute) error {
	if len(add)+len(remove) > 4*routeLimit {
		return errors.New("too many route mutations")
	}
	if len(add)+len(remove) == 0 {
		return nil
	}
	socket, err := openRouteSocket()
	if err != nil {
		return err
	}
	defer closeResource(socket)
	operations := make([]routeMutation, 0, len(add)+len(remove))
	removedByPrefix := make(map[netip.Prefix]managedRoute, len(remove))
	for _, r := range remove {
		removedByPrefix[r.Prefix] = r
	}
	replaced := make(map[managedRoute]struct{})
	for _, r := range add {
		flags := uint16(syscall.NLM_F_CREATE | syscall.NLM_F_EXCL)
		if old, ok := removedByPrefix[r.Prefix]; ok {
			flags = syscall.NLM_F_CREATE | syscall.NLM_F_REPLACE
			replaced[old] = struct{}{}
		}
		operations = append(operations, routeMutation{Kind: syscall.RTM_NEWROUTE, Flags: flags, Route: r})
	}
	addCount := len(operations)
	for _, r := range remove {
		if _, ok := replaced[r]; !ok {
			operations = append(operations, routeMutation{Kind: syscall.RTM_DELROUTE, Route: r})
		}
	}
	for first := 0; first < len(operations); {
		if err := ctx.Err(); err != nil {
			return err
		}
		last := first + 64
		if last > len(operations) {
			last = len(operations)
		}
		if first < addCount && last > addCount {
			last = addCount
		}
		waiting := make(map[uint32]uint16, last-first)
		var packet []byte
		for _, operation := range operations[first:last] {
			flags := uint16(syscall.NLM_F_REQUEST | syscall.NLM_F_ACK)
			flags |= operation.Flags
			raw, seq := socket.packet(operation.Kind, flags, routeBody(operation.Route))
			packet = append(packet, raw...)
			waiting[seq] = operation.Kind
		}
		if err := syscall.Sendto(socket.fd, packet, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK}); err != nil {
			return err
		}
		for len(waiting) > 0 {
			messages, err := socket.receive(ctx)
			if err != nil {
				return err
			}
			for _, message := range messages {
				kind, expected := waiting[message.Header.Seq]
				if !expected {
					continue
				}
				if message.Header.Type != syscall.NLMSG_ERROR || len(message.Data) < 4 {
					return errors.New("invalid route acknowledgement")
				}
				result := int32(binary.NativeEndian.Uint32(message.Data))
				if result != 0 {
					errno := syscall.Errno(-result)
					if kind != syscall.RTM_DELROUTE || errno != syscall.ESRCH && errno != syscall.ENOENT {
						return fmt.Errorf("kernel rejected split route operation: %w", errno)
					}
				}
				delete(waiting, message.Header.Seq)
			}
		}
		first = last
	}
	return nil
}
