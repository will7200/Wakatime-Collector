package main

import (
	"github.com/jinzhu/copier"
	"github.com/joomcode/errorx"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/cheggaaa/pb.v1"
	"net/http"
	"os"
	"os/signal"
	"path"
	"strings"
	"time"

	"github.com/alecthomas/kingpin"
	httptransport "github.com/go-openapi/runtime/client"
	"github.com/go-openapi/strfmt"
	"github.com/gregjones/httpcache/diskcache"
	"github.com/peterbourgon/diskv"
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

func init() {
	kingpin.Version(GitSummary + "; built on " + BuildDate)
	kingpin.Parse()

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
		slackHooker = NewSlackCore(*slackWebhook, encoder, config.Level.Level())
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

	/*opts := badger.DefaultOptions
	opts.Dir = path.Join(dir, "db")
	opts.ValueDir = path.Join(dir, "db")
	db, err := badger.Open(opts)
	if err != nil {
		logger.Panic(err.Error())
	}

	defer db.Close()

	txn := db.NewTransaction(true)
	defer txn.Discard()*/

	tp := NewHeuristicTransport(diskcache.NewWithDiskv(diskv.New(diskv.Options{
		BasePath:     leaderBoardDir,
		CacheSizeMax: 100 * 1024 * 1024,
	})))

	logger.Debug("Setting cached directory", zap.String("Cache-Directory",
		leaderBoardDir))

	runtime := httptransport.NewWithClient(defaultT.Host, defaultT.BasePath, defaultT.Schemes,
		&http.Client{Timeout: 10 * time.Second, Transport: tp})

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

	logger.Debug("Total Users Collected", zap.Int("acquired", total))

	bar = pb.StartNew(len(users))
	for key := range users {
		// mappedObject.lock.RLock()
		if users[key] {
			// mappedObject.lock.RUnlock()
			bar.Increment()
			continue
		}
		// mappedObject.lock.RUnlock()
		params := userclient.NewStatsParams()
		params.User = key
		_, accepted, err := client.User.Stats(params, apiKeyAuth)
		if accepted != nil {
			continue
		}
		if err != nil {
			if strings.Contains(err.Error(), "Client.Timeout") {
				continue
			}
			logger.Panic(err.Error())
		}
		mappedObject.lock.Lock()
		users[key] = true
		mappedObject.lock.Unlock()
		bar.Increment()
	}
	bar.Finish()
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
