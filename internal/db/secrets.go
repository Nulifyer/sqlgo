package db

import (
	"errors"

	"github.com/99designs/keyring"
)

type SecretStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Remove(key string) error
}

type KeyringSecretStore struct {
	ring keyring.Keyring
}

func NewSecretStore() (*KeyringSecretStore, error) {
	ring, err := keyring.Open(keyring.Config{
		ServiceName: "sqlgo",
	})
	if err != nil {
		return nil, err
	}
	return &KeyringSecretStore{ring: ring}, nil
}

func (s *KeyringSecretStore) Get(key string) (string, error) {
	item, err := s.ring.Get(key)
	if errors.Is(err, keyring.ErrKeyNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(item.Data), nil
}

func (s *KeyringSecretStore) Set(key, value string) error {
	return s.ring.Set(keyring.Item{
		Key:   key,
		Label: key,
		Data:  []byte(value),
	})
}

func (s *KeyringSecretStore) Remove(key string) error {
	if err := s.ring.Remove(key); errors.Is(err, keyring.ErrKeyNotFound) {
		return nil
	} else {
		return err
	}
}
