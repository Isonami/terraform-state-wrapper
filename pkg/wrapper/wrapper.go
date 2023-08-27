package wrapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"time"
)

type LockInfo struct {
	// Unique ID for the lock. NewLockInfo provides a random ID, but this may
	// be overridden by the lock implementation. The final value of ID will be
	// returned by the call to Lock.
	ID string

	// Terraform operation, provided by the caller.
	Operation string

	// Extra information to store with the lock, provided by the caller.
	Info string

	// user@hostname when available
	Who string

	// Terraform version
	Version string

	// Time that the lock was taken.
	Created time.Time

	// Path to the state file when applicable. Set by the Lock implementation.
	Path string
}

type Backend interface {
	Config(ctx context.Context) error
	Get(ctx context.Context) ([]byte, error)
	Set(ctx context.Context, data []byte, lockID, comment string) error
	Delete(ctx context.Context) error
	Lock(ctx context.Context, lockData LockInfo) (bool, LockInfo, error)
	UnLock(ctx context.Context, lockData LockInfo) error
	Lockable() bool
}

func createListener() (l net.Listener, close func(), err error) {
	l, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, err
	}
	return l, func() {
		_ = l.Close()
	}, nil
}

var letters = []rune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")

func randSeq(n int) string {
	seed := rand.New(rand.NewSource(time.Now().UnixNano()))

	b := make([]rune, n)
	for i := range b {
		b[i] = letters[seed.Intn(len(letters))]
	}
	return string(b)
}

func backendHandler(ctx context.Context, backend Backend, action, authUser, authPassword string) http.HandlerFunc {
	systemUser, _ := user.Current()

	comment := fmt.Sprintf("updated with terraform '%s' by '%s'", action, systemUser.Name)

	return func(writer http.ResponseWriter, request *http.Request) {
		userRequest, passwordRequest, ok := request.BasicAuth()
		if !ok || userRequest != authUser || passwordRequest != authPassword {
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
		}

		returnError := func(err error) {
			http.Error(writer, err.Error(), http.StatusInternalServerError)
		}

		switch request.Method {
		case http.MethodGet:
			data, err := backend.Get(ctx)
			if err != nil {
				returnError(err)
			}
			if data == nil {
				writer.WriteHeader(http.StatusNotFound)
				return
			}
			writer.WriteHeader(http.StatusOK)
			_, err = writer.Write(data)
			if err != nil {
				log.Println("failed to write response", err)
			}
			return
		case http.MethodPost:
			data, err := io.ReadAll(request.Body)
			if err != nil {
				returnError(err)
			}

			lockId := request.URL.Query().Get("ID")

			err = backend.Set(ctx, data, lockId, comment)
			if err != nil {
				returnError(err)
			}

			writer.WriteHeader(http.StatusOK)
			return
		case http.MethodDelete:
			err := backend.Delete(ctx)
			if err != nil {
				returnError(err)
			}

			writer.WriteHeader(http.StatusOK)
			return
		}

		if backend.Lockable() {
			switch request.Method {
			case "LOCK", "UNLOCK":
				data, err := io.ReadAll(request.Body)
				if err != nil {
					returnError(err)
				}

				lockData := LockInfo{}

				err = json.Unmarshal(data, &lockData)
				if err != nil {
					returnError(err)
				}

				if request.Method == "LOCK" {
					ok, lock, err := backend.Lock(ctx, lockData)
					if err != nil {
						returnError(err)
					}
					if !ok {
						data, err := json.Marshal(lock)
						if err != nil {
							returnError(err)
						}
						writer.WriteHeader(http.StatusConflict)
						_, err = writer.Write(data)
						if err != nil {
							log.Println("failed to write response", err)
						}
					}
				} else {
					err := backend.UnLock(ctx, lockData)
					if err != nil {
						returnError(err)
					}
				}

				writer.WriteHeader(http.StatusOK)
				return
			}
		}

		http.Error(writer, "invalid method", http.StatusMethodNotAllowed)
	}
}

func Wrap(ctx context.Context, backend Backend, args []string) {
	err := backend.Config(ctx)
	if err != nil {
		log.Fatal(err)
	}

	terraformAction := ""
	for _, arg := range args {
		if len(arg) > 0 && arg[0] != '-' {
			terraformAction = arg
			break
		}
	}

	listener, closeListener, err := createListener()
	if err != nil {
		log.Fatal(err)
	}
	defer closeListener()

	backendUrl := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port),
		Path:   "/backend",
	}

	authUser := "auth"
	authPassword := randSeq(32)

	mux := http.NewServeMux()
	mux.Handle(backendUrl.Path, backendHandler(ctx, backend, terraformAction, authUser, authPassword))

	go func() {
		closedErr := http.ErrServerClosed
		err := http.Serve(listener, mux)
		if errors.As(err, &closedErr) {
			return
		}
		if err != nil {
			log.Fatal(err)
		}
	}()

	err = os.Setenv("TF_HTTP_ADDRESS", backendUrl.String())
	if err != nil {
		log.Fatal(err)
	}

	err = os.Setenv("TF_HTTP_USERNAME", authUser)
	if err != nil {
		log.Fatal(err)
	}

	err = os.Setenv("TF_HTTP_PASSWORD", authPassword)
	if err != nil {
		log.Fatal(err)
	}

	if backend.Lockable() {
		err = os.Setenv("TF_HTTP_LOCK_ADDRESS", backendUrl.String())
		if err != nil {
			log.Fatal(err)
		}

		err = os.Setenv("TF_HTTP_UNLOCK_ADDRESS", backendUrl.String())
		if err != nil {
			log.Fatal(err)
		}
	}

	if len(args) > 0 {
		terraform := exec.CommandContext(ctx, args[0], args[1:]...)
		terraform.Stdout = os.Stdout
		terraform.Stderr = os.Stderr
		terraform.Stdin = os.Stdin

		var exitErr *exec.ExitError
		err = terraform.Run()
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		} else if err != nil {
			log.Fatal(err)
		}
		os.Exit(0)
	} else {
		log.Fatal("usage: ./wrapper terraform plan|apply|validate...")
	}
}
