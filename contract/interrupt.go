package flowcontract

type InterruptError struct {
	Payload interface{}
}

func (e *InterruptError) Error() string {
	return "interrupt"
}

func Interrupt(payload interface{}) error {
	return &InterruptError{Payload: payload}
}

func IsInterrupt(err error) (*InterruptError, bool) {
	if ie, ok := err.(*InterruptError); ok {
		return ie, true
	}
	return nil, false
}
