package executor

// RequestError identifies a failure caused by the current request rather than
// by the selected credential or provider availability.
type RequestError struct {
	HTTPStatus int
	Code       string
	Message    string
}

func (e *RequestError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *RequestError) StatusCode() int {
	if e == nil {
		return 0
	}
	return e.HTTPStatus
}

func (e *RequestError) IsRequestScoped() bool {
	return e != nil
}
