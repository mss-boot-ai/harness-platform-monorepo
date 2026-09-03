package domain

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const IDSize = 16

type ID [IDSize]byte

var ErrInvalidID = errors.New("invalid opaque ID")

func NewID(reader io.Reader) (ID, error) {
	if reader == nil {
		reader = rand.Reader
	}
	var id ID
	if _, err := io.ReadFull(reader, id[:]); err != nil {
		return ID{}, fmt.Errorf("generate opaque ID: %w", err)
	}
	if id.IsZero() {
		return ID{}, fmt.Errorf("%w: all-zero value", ErrInvalidID)
	}
	return id, nil
}

func ParseID(value string) (ID, error) {
	value = strings.TrimSpace(value)
	if len(value) != IDSize*2 {
		return ID{}, fmt.Errorf("%w: expected %d hexadecimal characters", ErrInvalidID, IDSize*2)
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return ID{}, fmt.Errorf("%w: %v", ErrInvalidID, err)
	}
	var id ID
	copy(id[:], decoded)
	if id.IsZero() {
		return ID{}, fmt.Errorf("%w: all-zero value", ErrInvalidID)
	}
	return id, nil
}

func (id ID) String() string { return hex.EncodeToString(id[:]) }

func (id ID) IsZero() bool {
	for _, value := range id {
		if value != 0 {
			return false
		}
	}
	return true
}

func (id ID) MarshalJSON() ([]byte, error) {
	if id.IsZero() {
		return nil, ErrInvalidID
	}
	return json.Marshal(id.String())
}

func (id *ID) UnmarshalJSON(data []byte) error {
	if id == nil {
		return ErrInvalidID
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidID, err)
	}
	parsed, err := ParseID(value)
	if err != nil {
		return err
	}
	*id = parsed
	return nil
}
