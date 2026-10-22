// loadtest measures concurrent room synchronization against an already running server.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sangjinsu/coopdock/internal/loadtest"
)

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	c := loadtest.DefaultConfig()
	flags := flag.NewFlagSet("loadtest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	games := strings.Join(c.GameIDs, ",")
	flags.StringVar(&c.URL, "url", c.URL, "대상 WebSocket base URL (query/fragment/credentials 금지)")
	flags.StringVar(&games, "game-ids", games, "서버 등록 game ID 목록, 콤마 구분; 룸을 순서대로 분배")
	flags.IntVar(&c.Rooms, "rooms", c.Rooms, "전체 룸 수 (1..512, 전체 client 최대 3072)")
	flags.IntVar(&c.Players, "players", c.Players, "룸당 참가자 수 (1..6)")
	flags.DurationVar(&c.Duration, "duration", c.Duration, "setup을 제외한 동시 측정 기간 (양수, 최대 1h)")
	flags.IntVar(&c.InputHz, "input-hz", c.InputHz, "각 client의 목표 입력 Hz (1..120), ACK 대기와 독립")
	flags.DurationVar(&c.SetupTimeout, "setup-timeout", c.SetupTimeout, "전체 setup deadline (양수, 최대 5m)")
	flags.DurationVar(&c.DrainTimeout, "drain-timeout", c.DrainTimeout, "측정 후 ACK drain 및 zero ACK/정지 확인 deadline (최대 5m)")
	flags.DurationVar(&c.CleanupTimeout, "cleanup-timeout", c.CleanupTimeout, "취소와 독립적인 전체 퇴장/빈 룸 정리 deadline (최대 5m)")
	flags.DurationVar(&c.RequestTimeout, "request-timeout", c.RequestTimeout, "개별 요청/쓰기 제한; 지속 reader는 취소하지 않음 (최대 5m)")
	flags.IntVar(&c.MaxPending, "max-pending", c.MaxPending, "client당 pending 상한 (1..4096, 전체 용량<=1048576); 초과 시 실패")
	flags.IntVar(&c.SampleLimit, "sample-limit", c.SampleLimit, "분포당 uniform reservoir 표본 상한 (1..100000, rooms*상한<=2000000)")
	flags.IntVar(&c.CaptureLimit, "capture-limit", c.CaptureLimit, "룸당 canonical capture window (2..4096, rooms*상한<=65536); 오래된 capture부터 제거")
	usage := func() {
		flags.SetOutput(stdout)
		defer flags.SetOutput(io.Discard)
		fmt.Fprintln(stdout, "사용법: loadtest [옵션]\nstdout: 실행 결과 JSON 한 줄. 실패/취소/정리 실패: exit 1. 목표 송신률과 실제 송신률은 report에서 구분합니다.")
		flags.PrintDefaults()
	}
	flags.Usage = func() {}
	printFailure := func(err error) {
		_ = json.NewEncoder(stdout).Encode(loadtest.Report{Success: false, Errors: []string{err.Error()}})
		fmt.Fprintln(stderr, err)
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			usage()
			return 0
		}
		printFailure(err)
		return 2
	}
	c.GameIDs = strings.Split(games, ",")
	for i := range c.GameIDs {
		c.GameIDs[i] = strings.TrimSpace(c.GameIDs[i])
	}
	if flags.NArg() != 0 {
		printFailure(errors.New("위치 인수는 지원하지 않습니다."))
		return 2
	}
	report, err := loadtest.Run(ctx, c)
	if encodeErr := json.NewEncoder(stdout).Encode(report); encodeErr != nil {
		fmt.Fprintln(stderr, encodeErr)
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, "부하 검증 실패:", err)
		return 1
	}
	return 0
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}
