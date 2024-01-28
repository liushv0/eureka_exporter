package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/fsnotify/fsnotify"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"gopkg.in/yaml.v3"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var cfg_file = flag.String("config.file", "./config.yaml", "path of config file")
var app_stat_metrics *prometheus.GaugeVec
var common_labels = []string{"eureka_instance", "eureka_application", "eureka_app_hostname", "eureka_app_status", "eureka_url"}
var extend_labels []string
var eureka_map = make(map[string]*EurekaConfig)
var status_cache = make(map[string][]*EurekaInstance)
var cache_lock sync.RWMutex

func main() {
	flag.Parse()
	fmt.Println("config file path: ", *cfg_file)
	if len(*cfg_file) == 0 {
		panic("config file can not be nil")
	}
	var config = &Config{}
	if f, err := os.Open(*cfg_file); err != nil {
		panic(err)
	}else {
		if content, err := io.ReadAll(f); err != nil {
			panic(err)
		}else {
			fmt.Println("eureka exporter config: " + string(content))
			if err = yaml.Unmarshal(content, config); err != nil {
				panic(err)
			}
		}
		_ = f.Close()
	}
	if result, msg := checkConfig(config); !result {
		panic(msg)
	}
	eureka_map = splitEurekaConfig(config)
	if len(eureka_map) == 0 {
		fmt.Println("no valid eureka instance, check config file..exit now")
		return
	}

	extend_labels = config.ExtendLabels

	var labels []string
	labels = append(labels, common_labels...)
	if len(extend_labels) > 0 {
		for _, el := range extend_labels {
			l := strings.ReplaceAll(el, "-", "_")
			l = "eureka_meta_" + l
			labels = append(labels, l)
		}
	}
	var metric = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "eureka_app_status", Help: "app register status on eureka"}, labels)

	register := prometheus.NewRegistry()
	register.MustRegister(metric)
	app_stat_metrics = metric

	startEurekaMonitor(nil, eureka_map)
	go monitorConfigYaml()

	http.Handle("/metrics", promhttp.HandlerFor(register, promhttp.HandlerOpts{}))
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html>
	        <head><title>eureka_exporter</title></head>
	        <body>
	        <h1>eureka_exporter</h1>
			<p>version: 0.0.1 </a></p>
	        <p><a href='metrics'>Metrics</a></p>
	        </body>
	        </html>`))
	})
	fmt.Println("will start eureka exporter listening...")
	err := http.ListenAndServe(":"+config.Port, nil)
	if err != nil {
		panic(err)
	}
}

func checkConfig(cfg *Config) (result bool, msg string) {
	if len(cfg.Port) <= 0 {
		cfg.Port = "9109"
	}
	if len(cfg.Eurekas) == 0 {
		return false, "eureka instance config is empty...exit now"
	}
	ecMap := make(map[string]*EurekaConfig)
	for idx, ec := range cfg.Eurekas {
		if len(ec.Urls) < 1 || len(ec.Name) < 1 {
			return false, "invalid eureka instance config: urls/name can not be empty"
		}
		if ecMap[ec.Name] != nil {
			return false, "duplicate eureka instance name: " + ec.Name
		}
		if ec.PullInterval <= 0 {
			fmt.Println("pull interval can not be negative, reset to 30 seconds, eureka instance name: " + ec.Name)
			cfg.Eurekas[idx].PullInterval = 30 * time.Second
		}
		ecMap[ec.Name] = &ec
	}
	return true, ""
}

func splitEurekaConfig(cfg *Config) map[string]*EurekaConfig  {
	result := make(map[string]*EurekaConfig)
	for _, ec := range cfg.Eurekas {
		if strings.Contains(ec.Urls, ",") {
			urls := strings.Split(ec.Urls, ",")
			for _, url := range urls {
				url = strings.TrimSpace(url)
				if len(url) > 0 {
					ins := &EurekaConfig{
						Urls:     url,
						Name:     ec.Name,
						Security: ec.Security,
						PullInterval: ec.PullInterval,
					}
					result[ec.Name + "_" + url] = ins
				}
			}
		}else {
			ins := &EurekaConfig{
				Urls:     ec.Urls,
				Name:     ec.Name,
				Security: ec.Security,
				PullInterval: ec.PullInterval,
			}
			result[ec.Name + "_" + ec.Urls] = ins
		}
	}
	return result
}

func startEurekaMonitor(oldIns map[string]*EurekaConfig, newIns map[string]*EurekaConfig) {
	if oldIns != nil {
		for _, ins := range oldIns {
			if ins.StopChan != nil {
				ins.StopChan <- 1
			}
		}
	}
	status_cache = make(map[string][]*EurekaInstance) 		//clean status cache
	app_stat_metrics.Reset()
	time.Sleep(time.Millisecond * 100)
	for _, ins := range newIns {
		ch := make(chan int, 1)
		ins.StopChan = ch
		go monitorAppStatus(ins)
	}
}

func monitorConfigYaml() {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		panic(err)
	}
	defer func(watcher *fsnotify.Watcher) {
		_ = watcher.Close()
	}(watcher)
	if err = watcher.Add(*cfg_file); err != nil {
		panic(err)
	}

	for  {
		select {
		case event := <- watcher.Events:
			reload := false
			if event.Has(fsnotify.Create) || event.Has(fsnotify.Write){
				fmt.Println("config file be created/write...reload now...")
				reload = true
			}
			if reload {
				var config = &Config{}
				if f, err := os.Open(*cfg_file); err != nil {
					panic(err)
				}else {
					if content, err := io.ReadAll(f); err != nil {
						panic(err)
					}else {
						fmt.Println("reload eureka exporter config: " + string(content))
						if err = yaml.Unmarshal(content, config); err != nil {
							panic(err)
						}
					}
					_ = f.Close()
				}
				if result, msg := checkConfig(config); !result {
					fmt.Println(msg)
					fmt.Println("eureka exporter config invalid, ignore && nothing changed...")
				}
				tmpMap := splitEurekaConfig(config)
				if len(tmpMap) == 0 {
					fmt.Println("no valid eureka instance, check config file..ignore && nothing changed...")
				}
				fmt.Println("restart eureka monitor goroutine...")
				startEurekaMonitor(eureka_map, tmpMap)
				eureka_map = tmpMap
			}
		case we := <-watcher.Errors:
			fmt.Println("config file watch error: " + we.Error())
		}
	}
}

func monitorAppStatus(eureka *EurekaConfig) {
	if strings.HasSuffix(eureka.Urls, "/") {
		eureka.Urls = eureka.Urls[:len(eureka.Urls)-1]
	}
	stat_url := fmt.Sprintf("%s/eureka/apps", eureka.Urls)
	fmt.Println(fmt.Sprintf("will start new goroutine, eureka instance:%s, url:%s, interval:%s", eureka.Name, eureka.Urls, eureka.PullInterval))
	ticker := time.NewTicker(eureka.PullInterval)
	for {
		select {
		case <-ticker.C:
			getAppStat(stat_url, eureka.Name, &eureka.Security)
		case <-eureka.StopChan:
			fmt.Println(fmt.Sprintf("eureka goroutine quit, instance:%s, url:%s, interval:%s", eureka.Name, eureka.Urls, eureka.PullInterval))
			return
		}
	}
}

func getAppStat(url, name string, sec *Security) {
	var err error
	defer func() {
		if err != nil {
			msg := fmt.Sprintf("eureka instance %s/%s get app status failed, error: %s", name, url, err.Error())
			fmt.Println(msg)
		}
	}()

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if sec != nil && len(sec.Basic.User) > 0 {
		req.SetBasicAuth(sec.Basic.User, sec.Basic.Password)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	if resp.StatusCode != 200 {
		msg := fmt.Sprintf("resp code is %d", resp.StatusCode)
		err = errors.New(msg)
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}
	eureka_resp := &EurekaResp{}
	err = json.Unmarshal(body, eureka_resp)
	if err != nil {
		return
	}

	var ins_list []*EurekaInstance
	for _, app := range eureka_resp.Applications.ApplicationList {
		for _, ins := range app.Instances {
			ins_list = append(ins_list, ins)
			values := getValues(name, url, ins)
			app_stat_metrics.WithLabelValues(values...).Set(1)
		}
	}

	offline := offlineInstance(url, ins_list)
	for _, ins := range offline {
		values := getValues(name, url, ins)
		app_stat_metrics.DeleteLabelValues(values...)
	}
	updateCache(url, ins_list)
}

//
func offlineInstance(url string, ins_new []*EurekaInstance) (result []*EurekaInstance) {
	cache_lock.RLock()
	defer cache_lock.RUnlock()
	ins_old := status_cache[url]
	if len(ins_new) == 0 {
		return ins_old
	}
	if len(ins_old) == 0 {
		return result
	}
	ins_new_str := make([]string, len(ins_new))
	for i, ins := range ins_new {
		byt, _ := json.Marshal(ins)
		ins_new_str[i] = string(byt)
	}
	for _, ins := range ins_old {
		byt, _ := json.Marshal(ins)
		ins_str := string(byt)
		online := false
		for _, str := range ins_new_str {
			if str == ins_str {
				online = true
				break
			}
		}
		if !online {
			result = append(result, ins)
		}
	}
	return
}

func updateCache(url string, ins_new []*EurekaInstance) {
	cache_lock.Lock()
	defer cache_lock.Unlock()
	status_cache[url] = ins_new
}

func getValues(name, url string, ins *EurekaInstance) []string {
	values := []string{name, ins.App, ins.HostName, ins.Status, url}
	for _, key := range extend_labels {
		v := ins.MetaInfo[key]
		if len(v) == 0 {
			v = ""
		}
		values = append(values, v)
	}
	return values
}

// Config ↓↓↓↓↓↓↓↓↓↓ parse eureka exporter yaml config ↓↓↓↓↓↓↓↓↓↓↓↓↓↓
type Config struct {
	Port string `yaml:"port"`
	ExtendLabels []string `yaml:"metadata"`
	Eurekas []EurekaConfig `yaml:"eurekas"`
}

type EurekaConfig struct {
	Urls string `yaml:"urls"`
	Name string `yaml:"name"`
	PullInterval time.Duration `yaml:"pullInterval"`
	Security Security `yaml:"security"`
	StopChan chan int
}

type Security struct {
	Basic BasicAuth `yaml:"basic"`
}

type BasicAuth struct {
	User string `yaml:"user"`
	Password string `yaml:"password"`
}

// EurekaResp ↓↓↓↓↓↓↓↓↓ parse eureka server response ↓↓↓↓↓↓↓↓↓↓
type EurekaResp struct {
	Applications *Applications `json:"applications"`
}

type Applications struct {
	ApplicationList []*Application `json:"application"`
	AppHashCode string `json:"apps__hashcode"`
	VersionDelta string `json:"versions__delta"`
}

type Application struct {
	Name      string            `json:"name"`
	Instances []*EurekaInstance `json:"instance"`
}

type EurekaInstance struct {
	//ActionType string `json:"actionType,omitempty"`
	App string `json:"app"`
	//CountryId int `json:"countryId,omitempty"`
	//HealthCheckUrl string `json:"healthCheckUrl,omitempty"`
	//HomePageUrl string `json:"homePageUrl,omitempty"`
	HostName string `json:"hostName"`
	InstanceId string `json:"instanceId"`
	IpAddr string `json:"ipAddr"`
	//LastDirtyTimestamp string `json:"lastDirtyTimestamp,omitempty"`
	//LastUpdatedTimestamp string `json:"lastUpdatedTimestamp,omitempty"`
	MetaInfo map[string]string `json:"metadata"`
	//OverriddenStatus string `json:"overriddenstatus"`
	Status string `json:"status"`
}

func statusToCode(status string) float64 {
	switch status {
	case "UP":
		return 0
	case "DOWN":
		return 1
	case "STARTING":
		return 2
	case "OUT_OF_SERVICE":
		return 3
	case "UNKNOWN":
		return 4
	default:
		return 5
	}
}