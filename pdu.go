package smpp34

import (
	"bytes"
	"encoding/binary"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"io"
	"reflect"
)

const (
	PduLenErr PduReadErr = "Invalid PDU length"
)

type PduReadErr string
type PduCmdIdErr string

type Pdu interface {
	Fields() map[string]Field
	MandatoryFieldsList() []string
	GetField(string) Field
	GetHeader() *Header
	TLVFields() map[uint16]*TLVField
	Writer() []byte
	SetField(f string, v interface{}) error
	SetTLVField(t, l int, v []byte) error
	SetSeqNum(uint32)
	Ok() bool
}

func (p PduReadErr) Error() string {
	return string(p)
}

func (p PduCmdIdErr) Error() string {
	return string(p)
}

func ParsePdu(data []byte) (Pdu, error) {
	if len(data) < 16 {
		return nil, PduLenErr
	}

	header := ParsePduHeader(data[:16])

	if int(header.Length) != len(data) {
		return nil, PduLenErr
	}

	switch header.Id {
	case SUBMIT_SM:
		n, err := NewSubmitSm(header, data[16:])
		return Pdu(n), err
	case SUBMIT_SM_RESP:
		n, err := NewSubmitSmResp(header, data[16:])
		return Pdu(n), err
	case DELIVER_SM:
		n, err := NewDeliverSm(header, data[16:])
		return Pdu(n), err
	case DELIVER_SM_RESP:
		n, err := NewDeliverSmResp(header, data[16:])
		return Pdu(n), err
	case BIND_TRANSCEIVER, BIND_RECEIVER, BIND_TRANSMITTER:
		n, err := NewBind(header, data[16:])
		return Pdu(n), err
	case BIND_TRANSCEIVER_RESP, BIND_RECEIVER_RESP, BIND_TRANSMITTER_RESP:
		n, err := NewBindResp(header, data[16:])
		return Pdu(n), err
	case ENQUIRE_LINK:
		n, err := NewEnquireLink(header)
		return Pdu(n), err
	case ENQUIRE_LINK_RESP:
		n, err := NewEnquireLinkResp(header)
		return Pdu(n), err
	case UNBIND:
		n, err := NewUnbind(header)
		return Pdu(n), err
	case UNBIND_RESP:
		n, err := NewUnbindResp(header)
		return Pdu(n), err
	case GENERIC_NACK:
		n, err := NewGenericNack(header)
		return Pdu(n), err
	case QUERY_SM:
		n, err := NewQuerySm(header, data[16:])
		return Pdu(n), err
	case QUERY_SM_RESP:
		n, err := NewQuerySmResp(header, data[16:])
		return Pdu(n), err
	default:
		return nil, PduCmdIdErr(header.Id.Error())
	}
}

func ParsePduHeader(data []byte) *Header {
	return NewPduHeader(
		unpackUi32(data[:4]),
		CMDId(unpackUi32(data[4:8])),
		CMDStatus(unpackUi32(data[8:12])),
		unpackUi32(data[12:16]),
	)
}

func create_pdu_fields(fieldNames []string, r *bytes.Buffer) (map[string]Field, map[uint16]*TLVField, error) {

	fields := make(map[string]Field)
	eof := false

	for _, k := range fieldNames {
		switch k {
		case SERVICE_TYPE, SOURCE_ADDR, DESTINATION_ADDR, SCHEDULE_DELIVERY_TIME, VALIDITY_PERIOD, SYSTEM_ID, PASSWORD, SYSTEM_TYPE, ADDRESS_RANGE, MESSAGE_ID, FINAL_DATE, MESSAGE_STATE, ERROR_CODE:
			// Review this for fields that could be 1 or 17 int in length (E.g: FINAL_DATE)
			t, err := r.ReadBytes(0x00)

			if err == io.EOF {
				eof = true
			} else if err != nil {
				return nil, nil, err
			}

			if len(t) == 0 {
				fields[k] = NewVariableField(t)
			} else {
				fields[k] = NewVariableField(t[:len(t)-1])
			}
		case SOURCE_ADDR_TON, SOURCE_ADDR_NPI, DEST_ADDR_TON, DEST_ADDR_NPI, ESM_CLASS, PROTOCOL_ID, PRIORITY_FLAG, REGISTERED_DELIVERY, REPLACE_IF_PRESENT_FLAG, DATA_CODING, SM_DEFAULT_MSG_ID, INTERFACE_VERSION, ADDR_TON, ADDR_NPI:
			t, err := r.ReadByte()

			if err == io.EOF {
				eof = true
			} else if err != nil {
				return nil, nil, err
			}

			if k == ESM_CLASS {

				// Setting up messaging mode
				for n, v := range ESM_MESSAGING_MODES {
					if fmt.Sprintf("%08b", t)[6:8] == v {
						fields[ESM_MESSAGE_MODE] = NewVariableField([]byte(n))
					}
				}

				// Setting up messaging mode
				for n, v := range ESM_MESSAGE_TYPES {
					if fmt.Sprintf("%08b", t)[2:6] == v {
						fields[ESM_MESSAGE_TYPE] = NewVariableField([]byte(n))
					}
				}

				// Setting up network type.
				switch t {
				case ESM_GSM_FEATURES[ESM_GSM_FEATURE_DEFAULT]:
					log.Debugf("Setting up GSM_NETWORK_TYPE to: %s", ESM_GSM_FEATURE_DEFAULT)
					fields[ESM_GSM_NETWORK_TYPE] = NewVariableField([]byte(ESM_GSM_FEATURE_DEFAULT))

				case ESM_GSM_FEATURES[ESM_GSM_FEATURE_UDHI]:
					log.Debugf("Setting up GSM_NETWORK_TYPE to: %s", ESM_GSM_FEATURE_UDHI)
					fields[ESM_GSM_NETWORK_TYPE] = NewVariableField([]byte(ESM_GSM_FEATURE_UDHI))

				case ESM_GSM_FEATURES[ESM_GSM_FEATURE_REPLY]:
					log.Debugf("Setting up GSM_NETWORK_TYPE to: %s", ESM_GSM_FEATURE_REPLY)
					fields[ESM_GSM_NETWORK_TYPE] = NewVariableField([]byte(ESM_GSM_FEATURE_REPLY))

				case ESM_GSM_FEATURES[ESM_GSM_FEATURE_UDHI_REPLY]:
					log.Debugf("Setting up GSM_NETWORK_TYPE to: %s", ESM_GSM_FEATURE_UDHI_REPLY)
					fields[ESM_GSM_NETWORK_TYPE] = NewVariableField([]byte(ESM_GSM_FEATURE_UDHI_REPLY))

				}

			}

			fields[k] = NewFixedField(t)
		case SM_LENGTH:
			// Short Message Length
			t, err := r.ReadByte()

			if err == io.EOF {
				eof = true
			} else if err != nil {
				return nil, nil, err
			}

			fields[k] = NewFixedField(t)

			// Short Message
			p := make([]byte, t)

			_, err = r.Read(p)
			if err == io.EOF {
				eof = true
			} else if err != nil {
				return nil, nil, err
			}

			msg := p

			for fi, va := range fields {
				if fi == ESM_GSM_NETWORK_TYPE {
					if va.String() == ESM_GSM_FEATURE_UDHI {

						// How many is there to count :)
						udhip := int(p[0]) + 1

						log.Debugf("This message is part of concat (header_len: %d) - (headers: %#v) - (message: %s)", udhip, p[:udhip], p[udhip:])

						msg = p[udhip:]
					}
				}
			}

			fields[SHORT_MESSAGE] = NewSMField(msg)
		case SHORT_MESSAGE:
			continue
		}
	}

	// Optional Fields
	tlvs := map[uint16]*TLVField{}
	var err error

	if !eof {
		tlvs, err = parse_tlv_fields(r)

		if err != nil {
			return nil, nil, err
		}
	}

	return fields, tlvs, nil
}

func parse_tlv_fields(r *bytes.Buffer) (map[uint16]*TLVField, error) {
	tlvs := map[uint16]*TLVField{}

	for {
		p := make([]byte, 4)
		_, err := r.Read(p)

		if err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}

		// length
		l := unpackUi16(p[2:4])

		// Get Value
		v := make([]byte, l)

		_, err = r.Read(v)
		if err != nil {
			return nil, err
		}

		tlvs[unpackUi16(p[0:2])] = &TLVField{
			unpackUi16(p[0:2]),
			unpackUi16(p[2:4]),
			v,
		}
	}

	return tlvs, nil
}

func validate_pdu_field(f string, v interface{}) bool {
	switch f {
	case SOURCE_ADDR_TON, SOURCE_ADDR_NPI, DEST_ADDR_TON, DEST_ADDR_NPI, ESM_CLASS, PROTOCOL_ID, PRIORITY_FLAG, REGISTERED_DELIVERY, REPLACE_IF_PRESENT_FLAG, DATA_CODING, SM_DEFAULT_MSG_ID, INTERFACE_VERSION, ADDR_TON, ADDR_NPI, SM_LENGTH, MESSAGE_STATE, ERROR_CODE:
		if validate_pdu_field_type(0x00, v) {
			return true
		}
	case SERVICE_TYPE, SOURCE_ADDR, DESTINATION_ADDR, SCHEDULE_DELIVERY_TIME, VALIDITY_PERIOD, SYSTEM_ID, PASSWORD, SYSTEM_TYPE, ADDRESS_RANGE, MESSAGE_ID, SHORT_MESSAGE, FINAL_DATE:
		if validate_pdu_field_type("string", v) {
			return true
		}
	}
	return false
}

func validate_pdu_field_type(t interface{}, v interface{}) bool {
	if reflect.TypeOf(t) == reflect.TypeOf(v) {
		return true
	}

	return false
}

func included_check(a []string, v string) bool {
	for _, k := range a {
		if k == v {
			return true
		}
	}
	return false
}

func unpackUi32(b []byte) (n uint32) {
	n = binary.BigEndian.Uint32(b)
	return
}

func packUi32(n uint32) (b []byte) {
	b = make([]byte, 4)
	binary.BigEndian.PutUint32(b, n)
	return
}

func unpackUi16(b []byte) (n uint16) {
	n = binary.BigEndian.Uint16(b)
	return
}

func packUi16(n uint16) (b []byte) {
	b = make([]byte, 2)
	binary.BigEndian.PutUint16(b, n)
	return
}

func packUi8(n uint8) (b []byte) {
	b = make([]byte, 2)
	binary.BigEndian.PutUint16(b, uint16(n))
	return b[1:]
}
