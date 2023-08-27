package file

import (
	"context"
	"errors"
	"github.com/Isonami/terraform-state-wrapper/pkg/wrapper"
	"os"
)

var _ wrapper.Backend = &Backend{}

type Backend struct {
	filePath string
}

func (f *Backend) Config(ctx context.Context) error {
	if value, ok := os.LookupEnv("TF_STATE_WRAPPER_FILE_PATH"); ok {
		f.filePath = value
		return nil
	}
	return errors.New("'TF_STATE_WRAPPER_FILE_PATH' must be set")
}

func (f *Backend) Get(ctx context.Context) ([]byte, error) {
	if _, err := os.Stat(f.filePath); errors.Is(err, os.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return os.ReadFile(f.filePath)
}

func (f *Backend) Set(ctx context.Context, data []byte, lockID, comment string) error {
	return os.WriteFile(f.filePath, data, 0644)
}

func (f *Backend) Delete(ctx context.Context) error {
	if _, err := os.Stat(f.filePath); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	return os.Remove(f.filePath)
}

func (f *Backend) Lock(ctx context.Context, lockData wrapper.LockInfo) (bool, wrapper.LockInfo, error) {
	return false, wrapper.LockInfo{}, nil
}

func (f *Backend) UnLock(ctx context.Context, lockData wrapper.LockInfo) error {
	return nil
}

func (f *Backend) Lockable() bool {
	return false
}
