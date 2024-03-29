package main

import (
	"bufio"
	"bytes"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/cenkalti/backoff"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/jinzhu/copier"
	"github.com/joomcode/errorx"
	"github.com/peterbourgon/diskv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/cheggaaa/pb.v1"

	apiclient "github.com/will7200/go-wakatime/client"
	"github.com/will7200/go-wakatime/client/leaders"
	userclient "github.com/will7200/go-wakatime/client/user"
	"github.com/will7200/go-wakatime/models"
)

var (
	leaderRange    = kingpin.Arg("range", "range pick from 7, 30, 180, 365").Default("7").Int()
	slackWebhook   = kingpin.Flag("slack-webhook", "webhook for slack errors").Envar("SLACK_HOOK").Short('w').String()
	wakatimeAPIKey = kingpin.Flag("wakatime-api-key", "wakatime api client key").Envar("WAKATIME_API_KEY").Short('k').String()
	verbose        = kingpin.Flag("verbose", "verbose level").Envar("COLLECTOR_VERBOSE").Short('v').Bool()
	clientTimeout  = kingpin.Flag("http-timeout", "http client timeout").Default("10").Int()

	BuildDate  string
	GitCommit  string
	GitBranch  string
	GitState   string
	GitSummary string
)

var (
	logger       *zap.Logger
	slackHooker  *SlackCore
	mappedObject DiskMappedObject
)

var (
	usersFile = "allusers.array"
)

func init() {
	kingpin.Version(GitSummary + "; built on " + BuildDate)
	kingpin.CommandLine.Parse(os.Args[1:])

	config := zap.Config{
		Level:       zap.NewAtomicLevelAt(zap.DebugLevel),
		Development: false,
		Encoding:    "console",
		// EncoderConfig:    zap.NewDevelopmentEncoderConfig(),
		OutputPaths:      []string{"stderr"},
		ErrorOutputPaths: []string{"stderr"},
	}
	var err error
	var options []zap.Option
	var cores []zapcore.Core
	if *verbose {
		config.EncoderConfig = zap.NewDevelopmentEncoderConfig()
	} else {
		config.EncoderConfig = zap.NewProductionEncoderConfig()
		zap.NewAtomicLevelAt(zap.ErrorLevel)
	}
	encoder := zapcore.NewConsoleEncoder(config.EncoderConfig)

	consoleDebugging := zapcore.Lock(os.Stdout)
	consoleErrors := zapcore.Lock(os.Stderr)
	highPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl >= zapcore.ErrorLevel
	})
	lowPriority := zap.LevelEnablerFunc(func(lvl zapcore.Level) bool {
		return lvl < zapcore.ErrorLevel
	})

	cores = append(cores, zapcore.NewCore(encoder, consoleErrors, highPriority))
	cores = append(cores, zapcore.NewCore(encoder, consoleDebugging, lowPriority))
	if *slackWebhook != "" {
		var config1 zapcore.EncoderConfig
		if err := copier.Copy(&config1, &config.EncoderConfig); err != nil {
			panic(err)
		}
		config1.TimeKey = ""
		config1.LevelKey = ""
		config1.CallerKey = ""
		encoder := NewKVEncoder(config1)
		slackHooker = NewSlackCore(*slackWebhook, encoder, zap.InfoLevel)
		cores = append(cores, slackHooker)
	}
	options = append(options, zap.WrapCore(func(core zapcore.Core) zapcore.Core {
		return zapcore.NewTee(cores...)
	}))
	logger, err = config.Build(options...)
	if err != nil {
		panic(err)
	}
}

func rangeLeaderBoardString() string {
	switch *leaderRange {
	case 7:
		return string(models.RangeLast7Days)
	case 30:
		return string(models.RangeLast30Days)
	case 180:
		return string(models.RangeLast6Months)
	case 365:
		return string(models.RangeLastYear)
	default:
		return string(models.RangeLast7Days)
	}
}

func main() {
	c := make(chan os.Signal, 1)

	go func() {
		signal.Notify(c, os.Interrupt)
		<-c
		mappedObject.ForceSync()
		os.Exit(1)
	}()

	run()
	mappedObject.ForceSync()
}

func run() {
	{
		name, err := os.Hostname()
		if err != nil {
			panic(err)
		}
		logger.Info("Starting Collector", zap.String("node", name), zap.String("version", GitSummary))
	}

	defer logger.Sync()

	// setup client
	defaultT := apiclient.DefaultTransportConfig()

	// cache the requests since i want to retrieve them later
	rangeLeaderBoard := rangeLeaderBoardString()

	dir := path.Join(".cache-" + time.Now().Format("2006-01-02"))
	leaderBoardDir := path.Join(dir, rangeLeaderBoard)

	// Setup a disk back request cache note that this is a very aggressive caching method and it doesn't follow normal standards
	tp := NewHeuristicTransport(
		diskcache.NewWithDiskv(
			diskv.New(
				diskv.Options{
					BasePath:     leaderBoardDir,
					CacheSizeMax: 100 * 1024 * 1024,
				})))

	logger.Debug("Setting cached directory", zap.String("Cache-Directory",
		leaderBoardDir))

	runtime := httptransport.NewWithClient(defaultT.Host, defaultT.BasePath, defaultT.Schemes,
		&http.Client{Timeout: time.Duration(*clientTimeout) * time.Second, Transport: tp})

	client := apiclient.New(runtime, strfmt.Default)
	apiKeyAuth := httptransport.APIKeyAuth("api_key", "query", *wakatimeAPIKey)

	_, err := client.User.User(nil, apiKeyAuth)
	if err != nil {
		logger.Fatal(err.Error())
	}
	// fmt.Printf("%# v\n", pretty.Formatter(user))
	params := userclient.NewStatsParams()
	params.Range = string(models.RangeLast7Days)
	_, _, err = client.User.Stats(params, apiKeyAuth)
	if err != nil {
		logger.Fatal(err.Error())
	}
	// fmt.Printf("%# v\n", pretty.Formatter(use))

	// Leader boards
	params2 := leaders.NewLeaderParams()
	var start int64 = 1
	params2.Page = &start
	leader, err := client.Leaders.Leader(params2, apiKeyAuth)
	if err != nil {
		logger.Fatal(err.Error())
	}

	bar := pb.New(int(leader.Payload.TotalPages))
	users := make(map[string]bool, bar.Total*100)
	addUsersFromArray(users)
	filename := path.Join(dir, "users.tmp")
	mappedObject = DiskMappedObject{
		file:   filename,
		mapped: &users,
	}
	mappedObject.Read()
	defer mappedObject.Sync()
	if err := mappedObject.PeriodicWrite(time.Second * 60); err != nil {
		err = errorx.InitializationFailed.New("Failed to start synced users object")
		logger.Fatal(err.Error())
	}
	logger.Debug("Estimating total users", zap.Int64("users", bar.Total*100))

	addUsers(leader, users, mappedObject)
	writeAllUsers(users)

	bar.Start()
	bar.Increment()
	for {
		leader, err := client.Leaders.Leader(params2, apiKeyAuth)
		if err != nil {
			panic(err)
		}
		addUsers(leader, users, mappedObject)
		if *params2.Page == leader.Payload.TotalPages {
			break
		}
		bar.Increment()
		*params2.Page = *params2.Page + 1
	}
	bar.Finish()

	logger.Debug("Actual total users", zap.Int64("users", int64(len(users))))

	total := 0
	for _, val := range users {
		if val {
			total += 1
		}
	}

	logger.Info("Total Users Collected", zap.Int("acquired", total))
	logger.Info("Remaining Users to be collected", zap.Int("remaining", int(len(users)-total)))

	skippedTimeout := 0
	skippedAccepted := 0

	expBackOff := backoff.NewExponentialBackOff()
	expBackOff.InitialInterval = 1 * time.Second
	expBackOff.MaxInterval = 15 * time.Minute
	expBackOff.MaxElapsedTime = 1 * time.Hour
	expBackOff.Reset()

	bar = pb.StartNew(len(users))
	for key := range users {
		if users[key] {
			bar.Increment()
			continue
		}
		// expBackOff.Reset()
	retry:
		params := userclient.NewStatsParams()
		params.User = key
		_, accepted, err := client.User.Stats(params, apiKeyAuth)
		if accepted != nil {
			skippedAccepted += 1
			expBackOff.Reset()
			continue
		}
		if err != nil {
			err = parseError(err)
			switch {
			case errorx.IsOfType(err, RateLimited):
				duration := expBackOff.NextBackOff()
				if duration == backoff.Stop {
					expBackOff.Reset()
				} else {
					time.Sleep(duration)
				}
				goto retry
			case errorx.IsOfType(err, Timeout):
				skippedTimeout += 1
				continue
			case errorx.IsOfType(err, NotFound):
				continue
			}
			logger.Error(err.Error())
		}
		mappedObject.lock.Lock()
		users[key] = true
		mappedObject.lock.Unlock()
		bar.Increment()
		expBackOff.Reset()
	}
	bar.Finish()

	if skippedTimeout > 0 {
		logger.Info("Skipped some due to non 200 responses", zap.Int("skipped", skippedTimeout))
	}
	if skippedAccepted > 0 {
		logger.Info("Skipped some due to timeouts", zap.Int("skipped", skippedAccepted))
	}

	{
		total := 0
		for _, val := range users {
			if val {
				total += 1
			}
		}
		logger.Info("Total Users Collected", zap.Int("users", total), zap.Int("of", len(users)))
	}
}

func addUsers(leaderboard *leaders.LeaderOK, mapusers map[string]bool, m DiskMappedObject) {
	for _, data := range leaderboard.Payload.Data {
		m.lock.RLock()
		_, ok := mapusers[data.User.ID]
		m.lock.RUnlock()
		if !ok {
			m.lock.Lock()
			mapusers[data.User.ID] = false
			m.lock.Unlock()
		}
	}
}

func addUsersFromArray(mapusers map[string]bool) {
	var users []string
	users = make([]string, 0, 5000)
	if _, err := os.Stat(usersFile); err == nil {
		if err := Load(usersFile, &users, GlobDecoder); err != nil {
			panic(err.Error())
		}
	}
	for _, val := range users {
		mapusers[val] = false
	}
}

func writeAllUsers(m map[string]bool) {
	logger.Sugar().Debug("Total in array ", len(m))
	users := make([]string, len(m))
	i := 0
	for key, _ := range m {
		users[i] = key
		i += 1
	}
	b, err := GlobEncoder(users)
	if err != nil {
		panic(err)
	}
	err = writeAtomically(usersFile, func(w io.Writer) error {
		bb := b.(*bytes.Buffer)
		_, err := w.Write(bb.Bytes())
		return err
	})
	if err != nil {
		panic(err)
	}
}

func writeAtomically(dest string, write func(w io.Writer) error) (err error) {
	f, err := ioutil.TempFile("", "atomic-")
	if err != nil {
		return err
	}
	defer func() {
		// Clean up (best effort) in case we are returning with an error:
		if err != nil {
			// Prevent file descriptor leaks.
			f.Close()
			// Remove the tempfile to avoid filling up the file system.
			os.Remove(f.Name())
		}
	}()

	// Use a buffered writer to minimize write(2) syscalls.
	bufw := bufio.NewWriter(f)

	w := io.Writer(bufw)

	if err := write(w); err != nil {
		return err
	}

	if err := bufw.Flush(); err != nil {
		return err
	}

	// Chmod the file world-readable (ioutil.TempFile creates files with
	// mode 0600) before renaming.
	if err := f.Chmod(0644); err != nil {
		return err
	}

	// fsync(2) after fchmod(2) orders writes as per
	// https://lwn.net/Articles/270891/. Can be skipped for performance
	// for idempotent applications (which only ever atomically write new
	// files and tolerate file loss) on an ordered file systems. ext3,
	// ext4, XFS, Btrfs, ZFS are ordered by default.
	f.Sync()

	err = os.Rename(f.Name(), dest)
	if err != nil {
		if strings.Contains(err.Error(), "invalid cross-device") {
			defer os.Remove(f.Name())
			ff, err := os.Create(dest)
			if err != nil {
				return err
			}
			f.Sync()
			f.Seek(0, 0)
			_, err = io.Copy(ff, f)
			if err != nil {
				return err
			}
			err = ff.Sync()
			if err != nil {
				return err
			}
			err = ff.Close()
			return err
		}
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}
