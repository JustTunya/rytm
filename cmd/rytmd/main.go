package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"rytm/internal/ipc"
	"rytm/internal/queue"
	"rytm/internal/resolve"
	"strings"
	"syscall"
	"context"
	"time"
)

func main() {
	var binPaths []string
	if execPath, err := os.Executable(); err == nil {
		binDir := filepath.Dir(execPath)
		binPaths = append(binPaths, binDir)

		os.Chdir(filepath.Dir(binDir))
	}
	binPaths = append(binPaths, filepath.Join(".", "bin"))

	pathEnv := os.Getenv("PATH")
	for _, bp := range binPaths {
		if abs, err := filepath.Abs(bp); err == nil {
			if info, err := os.Stat(abs); err == nil && info.IsDir() {
				pathEnv = abs + string(os.PathListSeparator) + pathEnv
			}
		}
	}
	os.Setenv("PATH", pathEnv)

	socketPath := ipc.SocketPath

	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating socket directory: %v\n", err)
		os.Exit(1)
	}

	_ = os.Remove(socketPath)

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error starting socket listener: %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()
	defer os.Remove(socketPath)

	provider := resolve.NewInnerTubeProvider()
	scorer := resolve.DefaultScoring{}
	resolver := resolve.NewResolver(provider, scorer)
	manager := queue.NewManager(resolver)
	defer manager.Shutdown()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\nShutting down rytmd daemon...")
		manager.Shutdown()
		listener.Close()
		_ = os.Remove(socketPath)
		os.Exit(0)
	}()

	fmt.Printf("rytmd daemon listening on %s\n", socketPath)

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-sigChan:
				return
			default:
				fmt.Fprintf(os.Stderr, "Error accepting connection: %v\n", err)
				continue
			}
		}

		go handleConnection(conn, manager, resolver)
	}
}

func handleConnection(conn net.Conn, mgr *queue.Manager, resolver *resolve.Resolver) {
	defer conn.Close()

	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)

	for {
		var req ipc.Request
		if err := decoder.Decode(&req); err != nil {
			return
		}

		var resp ipc.Response
		switch req.Command {
		case ipc.CmdDownload:
			if req.Query == "" {
				resp = ipc.Response{Success: false, Error: "missing query"}
			} else {
				taskID := mgr.Submit(req.Query)
				resp = ipc.Response{Success: true, TaskID: taskID}
			}

		case ipc.CmdStatus:
			if req.TaskID != "" {
				if status, exists := mgr.GetTask(req.TaskID); exists {
					resp = ipc.Response{Success: true, Status: &status}
				} else {
					resp = ipc.Response{Success: false, Error: "task not found"}
				}
			} else {
				tasks := mgr.GetAllTasks()
				resp = ipc.Response{Success: true, Tasks: tasks}
			}

		case ipc.CmdCancel:
			if req.TaskID == "" {
				resp = ipc.Response{Success: false, Error: "missing task_id"}
			} else {
				ok := mgr.Cancel(req.TaskID)
				resp = ipc.Response{Success: ok}
				if !ok {
					resp.Error = "task not found or already completed/cancelled"
				}
			}

		case ipc.CmdResolve:
			if req.Query == "" {
				resp = ipc.Response{Success: false, Error: "missing query"}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				fullResult, err := resolver.ResolveAll(ctx, req.Query)
				if err != nil {
					resp = ipc.Response{Success: false, Error: fmt.Sprintf("resolve: %v", err)}
				} else {
					candidates := make([]ipc.ResolveCandidate, 0, len(fullResult.Candidates))
					for _, sc := range fullResult.Candidates {
						candidates = append(candidates, ipc.ResolveCandidate{
							URL:         sc.Entity.URL(),
							Title:       sc.Entity.Title,
							Artist:      strings.Join(sc.Entity.Artists, ", "),
							Album:       sc.Entity.Album,
							Type:        sc.Entity.Type.String(),
							Score:       sc.Score,
						})
					}
					resp = ipc.Response{
						Success: true,
						Resolve: &ipc.ResolveResponse{
							Confident:  fullResult.Confidence == resolve.ConfidenceHigh,
							Candidates: candidates,
						},
					}
				}
			}

		case ipc.CmdSubmitURL:
			if req.Query == "" {
				resp = ipc.Response{Success: false, Error: "missing url"}
			} else {
				taskID := mgr.Submit(req.Query)
				// We inject the ResolvedURL right away if possible
				if t, ok := mgr.GetTask(taskID); ok {
					// We need to set it on the task struct, but it's simpler to let downloader.go use Query as URL since it's already a URL
					_ = t
				}
				resp = ipc.Response{Success: true, TaskID: taskID}
			}

		default:
			resp = ipc.Response{Success: false, Error: "unknown command"}
		}

		if err := encoder.Encode(resp); err != nil {
			return
		}
	}
}
