package db

type invalidProfileError string

func (e invalidProfileError) Error() string {
	return string(e)
}

func ErrInvalidProfile(message string) error {
	return invalidProfileError(message)
}
