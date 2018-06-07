package smpp34

import (
	log "github.com/sirupsen/logrus"
	"time"
)

type Transmitter struct {
	Smpp
	eLTicker     *time.Ticker // Enquire Link ticker
	eLCheckTimer *time.Timer  // Enquire Link Check timer
	eLDuration   int          // Enquire Link Duration
	Err          error        // Errors generated in go routines that lead to conn close
}

// eli = EnquireLink Interval in Seconds
func NewTransmitter(host string, port int, eli int, bindParams Params) (*Transmitter, error) {
	tx := &Transmitter{}
	if err := tx.Connect(host, port); err != nil {
		return nil, err
	}

	sysId := bindParams[SYSTEM_ID].(string)
	pass := bindParams[PASSWORD].(string)

	if err := tx.Bind(sysId, pass, &bindParams); err != nil {
		return nil, err
	}

	// EnquireLinks should not be less 10seconds
	if eli < 10 {
		eli = 10
	}

	tx.eLDuration = eli

	go tx.StartEnquireLink(eli)

	return tx, nil
}

func (t *Transmitter) Bind(system_id string, password string, params *Params) error {
	pdu, err := t.Smpp.Bind(BIND_TRANSMITTER, system_id, password, params)
	if err := t.Write(pdu); err != nil {
		return err
	}

	// If BindResp NOT received in 5secs close connection
	go t.bindCheck()

	// Read (blocking)
	pdu, err = t.Smpp.Read()

	if err != nil {
		return err
	}

	if pdu.GetHeader().Id != BIND_TRANSMITTER_RESP {
		return SmppBindRespErr
	}

	if !pdu.Ok() {
		return SmppBindAuthErr("Bind auth failed. " + pdu.GetHeader().Status.Error())
	}

	t.Bound = true

	return nil
}

func (t *Transmitter) SubmitSm(source_addr, destination_addr, short_message string, params *Params) (seq uint32, err error) {
	p, err := t.Smpp.SubmitSm(source_addr, destination_addr, short_message, params)

	if err != nil {
		return 0, err
	}

	if err := t.Write(p); err != nil {
		return 0, err
	}

	return p.GetHeader().Sequence, nil
}

func (t *Transmitter) QuerySm(message_id, source_addr string, params *Params) (seq uint32, err error) {
	p, err := t.Smpp.QuerySm(message_id, source_addr, params)

	if err != nil {
		return 0, err
	}

	if err := t.Write(p); err != nil {
		return 0, err
	}

	return p.GetHeader().Sequence, nil
}

func (t *Transmitter) DeliverSmResp(seq, status uint32) error {
	return SmppPduErr
}

func (t *Transmitter) Unbind() error {
	p, _ := t.Smpp.Unbind()

	if err := t.Write(p); err != nil {
		return err
	}

	return nil
}

func (t *Transmitter) UnbindResp(seq uint32) error {
	p, _ := t.Smpp.UnbindResp(seq)

	if err := t.Write(p); err != nil {
		return err
	}

	t.Bound = false

	return nil
}

func (t *Transmitter) bindCheck() {
	// Block
	<-time.After(time.Duration(5 * time.Second))
	if !t.Bound {
		// send error to t.err? So it can be read before closing.
		t.Err = SmppBindRespErr
		t.Close()
	}
}

func (t *Transmitter) StartEnquireLink(eli int) {
	t.eLTicker = time.NewTicker(time.Duration(eli) * time.Second)
	// check delay is half the time of enquire link intervel
	d := time.Duration(eli/2) * time.Second
	t.eLCheckTimer = time.NewTimer(d)
	t.eLCheckTimer.Stop()

	for {
		select {
		case <-t.eLTicker.C:

			p, _ := t.EnquireLink()
			if err := t.Write(p); err != nil {
				log.Debugln("[Transmitter.StartEnquireLink] error writing EnquireLink. Closing connection:", err)
				t.Err = SmppELWriteErr
				t.Close()
				return
			}

			if t.eLCheckTimer != nil {
				t.eLCheckTimer.Reset(d)
			}
		case <-t.eLCheckTimer.C:
			t.Err = SmppELRespErr
			log.Debugln("[Transmitter.StartEnquireLink] timeout waiting for EnquireLinkResp. Closing connection:")
			t.Close()
			return
		}
	}
}

func (t *Transmitter) Read() (Pdu, error) {
	pdu, err := t.Smpp.Read()
	if err != nil {
		if _, ok := err.(PduCmdIdErr); ok {
			// Invalid PDU Command ID, should send back GenericNack
			t.GenericNack(uint32(0), ESME_RINVCMDID)
		} else if SmppPduLenErr == err {
			// Invalid PDU, PDU read or Len error
			t.GenericNack(uint32(0), ESME_RINVCMDLEN)
		}

		return nil, err
	}

	switch pdu.GetHeader().Id {
	case SUBMIT_SM_RESP:
		return pdu, nil
	case QUERY_SM_RESP:
		return pdu, nil
	case ENQUIRE_LINK:
		p, _ := t.Smpp.EnquireLinkResp(pdu.GetHeader().Sequence)

		if err := t.Write(p); err != nil {
			return nil, err
		}
	case ENQUIRE_LINK_RESP:
		// Reset EnquireLink Check
		if t.eLCheckTimer != nil {
			t.eLCheckTimer.Reset(time.Duration(t.eLDuration) * time.Second)
		}
	case UNBIND:
		t.UnbindResp(pdu.GetHeader().Sequence)
		t.Close()
	default:
		// Should not have received these PDUs on a TX bind
		return nil, SmppPduErr
	}

	return pdu, nil
}

func (t *Transmitter) Close() {
	// Check timers exists incase we Close() before timers are created
	if t.eLCheckTimer != nil {
		t.eLCheckTimer.Stop()
	}

	if t.eLTicker != nil {
		t.eLTicker.Stop()
	}

	// Make sure we unbind if we are binded first BEFORE we do any connection closure
	if t.Bound {
		t.Unbind()
	}

	t.Smpp.Close()
}

func (t *Transmitter) Write(p Pdu) error {
	err := t.Smpp.Write(p)

	return err
}
