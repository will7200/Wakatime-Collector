package main

import (
	"go.uber.org/zap"
	"net/http"
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
	slackWebhook   = kingpin.Flag("slack-webhook", "webhook for slack errors").Short('w').String()
	wakatimeAPIKey = kingpin.Flag("wakatime-api-key", "wakatime api client key").Envar("WAKATIME_API_KEY").Short('k').String()
	verbose        = kingpin.Flag("verbose", "verbose level").Short('v').Bool()

	version string
)

var (
	logger      *zap.Logger
	slackHooker *SlackHook
)

func init() {
	kingpin.Version(version)
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
	if *verbose {
		config.EncoderConfig = zap.NewDevelopmentEncoderConfig()
	} else {
		config.EncoderConfig = zap.NewProductionEncoderConfig()
		zap.NewAtomicLevelAt(zap.ErrorLevel)
	}
	if *slackWebhook != "" {
		slackHooker = NewSlackHook(*slackWebhook, config.Level.Level())
		options = append(options, zap.Hooks(slackHooker.GetHook()))
	}
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

	logger.Info("Starting Collector")
	// setup client
	defaultT := apiclient.DefaultTransportConfig()

	// cache the requests since i want to retrieve them later
	rangeLeaderBoard := rangeLeaderBoardString()

	tp := NewHuersticTransport(diskcache.NewWithDiskv(diskv.New(diskv.Options{
		BasePath:     ".cache-" + time.Now().Format("2006-01-02") + "/" + rangeLeaderBoard,
		CacheSizeMax: 100 * 1024 * 1024,
	})))

	logger.Debug("Setting cached directory", zap.String("Cache-Directory",
		".cache-"+time.Now().Format("2006-01-02")+"/"+rangeLeaderBoard))

	runtime := httptransport.NewWithClient(defaultT.Host, defaultT.BasePath, defaultT.Schemes,
		&http.Client{Timeout: 10 * time.Second, Transport: tp})

	client := apiclient.New(runtime, strfmt.Default)
	apiKeyAuth := httptransport.APIKeyAuth("api_key", "query", *wakatimeAPIKey)

	_, err := client.User.User(nil, apiKeyAuth)
	if err != nil {
		logger.Panic(err.Error())
	}
	// fmt.Printf("%# v\n", pretty.Formatter(user))
	params := userclient.NewStatsParams()
	params.Range = string(models.RangeLast7Days)
	_, _, err = client.User.Stats(params, apiKeyAuth)
	if err != nil {
		logger.Panic(err.Error())
	}
	// fmt.Printf("%# v\n", pretty.Formatter(use))

	// Leader boards
	/*params2 := leaders.NewLeaderParams()
	var start int64 = 1
	params2.Page = &start
	leader, err := client.Leaders.Leader(params2, apiKeyAuth)
	if err != nil {
		panic(err)
	}
	bar := pb.StartNew(int(leader.Payload.TotalPages))
	users := make(map[string]bool, bar.Total*100)
	addUsers(leader, users)
	bar.Increment()
	for ; ; {
		leader, err := client.Leaders.Leader(params2, apiKeyAuth)
		if err != nil {
			panic(err)
		}
		addUsers(leader, users)
		if *params2.Page == leader.Payload.TotalPages {
			break
		}
		bar.Increment()
		*params2.Page = *params2.Page + 1
	}
	bar.Finish()
	bar = pb.StartNew(len(users))
	for key := range users {
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
			panic(err)
		}
		users[key] = true
		bar.Increment()
	}
	bar.Finish()*/
}

func addUsers(leaderboard *leaders.LeaderOK, mapusers map[string]bool) {
	for _, data := range leaderboard.Payload.Data {
		mapusers[data.User.ID] = false
	}
}
