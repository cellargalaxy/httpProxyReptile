package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/gin-gonic/gin"
	"github.com/parnurzeal/gorequest"
	"github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

var dataPath = "data.json"
var address = ":8080"
var log = logrus.New()
var timeout = 5 * time.Second
var retry = 3
var pageMax = 5

var checkProxyMaxThread = 16
var checkProxySignal = make(chan int, checkProxyMaxThread)

var globalProxiesLock = make(chan int, 1)
var globalProxies []string
var globalProxiesMap = make(map[string]string)

func init() {
	loadGlobalProxies()
}

func main() {
	go autoFlushProxy()
	startWebService()
}

//----------------------------------------------------------------------------------------------------------------------

func startWebService() {
	log.Info("开始web服务")
	engine := gin.Default()
	engine.POST("/add", func(context *gin.Context) {
		proxiesString := context.PostForm("proxies")
		log.WithFields(logrus.Fields{"proxiesString": proxiesString}).Info("添加代理数据json")
		context.JSON(http.StatusOK, createResponseData(proxiesString, addProxyByJson(proxiesString)))
	})
	engine.GET("/get", func(context *gin.Context) {
		context.JSON(http.StatusOK, createResponseData(getProxy(), nil))
	})
	engine.GET("/list", func(context *gin.Context) {
		context.JSON(http.StatusOK, createResponseData(globalProxies, nil))
	})
	engine.Run(address)
	log.Info("结束web服务")
}

func createResponseData(data interface{}, err error) interface{} {
	if err == nil {
		return gin.H{"code": 1, "massage": err, "data": data}
	} else {
		return gin.H{"code": 2, "massage": err, "data": data}
	}
}

//----------------------------------------------------------------------------------------------------------------------

func getProxy() string {
	proxy := ""
	globalProxiesLock <- 1
	if len(globalProxies) > 0 {
		proxy = globalProxies[rand.Intn(len(globalProxies))]
	}
	<-globalProxiesLock
	return proxy
}

func addProxy(proxy string) {
	if proxy == "" {
		return
	}
	globalProxiesLock <- 1
	if _, ok := globalProxiesMap[proxy]; ok {
		return
	}
	globalProxiesMap[proxy] = proxy
	globalProxies = append(globalProxies, proxy)
	<-globalProxiesLock
}

func addProxies(proxies []string) {
	if proxies == nil || len(proxies) == 0 {
		return
	}
	globalProxiesLock <- 1
	for i := range proxies {
		if _, ok := globalProxiesMap[proxies[i]]; ok {
			continue
		}
		globalProxiesMap[proxies[i]] = proxies[i]
		globalProxies = append(globalProxies, proxies[i])
	}
	<-globalProxiesLock
}

func loadGlobalProxies() error {
	jsonString, err := readFileOrCreateIfNotExist(dataPath, "[]")
	if err != nil {
		return err
	}
	err = addProxyByJson(jsonString)
	if err != nil {
		return err
	}
	var proxies []string
	for i := range globalProxies {
		proxies = append(proxies, strings.Split(globalProxies[i], "//")[1])
	}
	globalProxies = []string{}
	globalProxiesMap = make(map[string]string)
	checkProxies(proxies)
	return saveGlobalProxies()
}

func addProxyByJson(jsonString string) error {
	var proxies []string
	err := json.Unmarshal([]byte(jsonString), &proxies)
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("反序列化代理数据json失败")
		return err
	}
	log.WithFields(logrus.Fields{"proxies": proxies}).Info("反序列化代理数据json成功")
	addProxies(proxies)
	return nil
}

func saveGlobalProxies() error {
	globalProxiesLock <- 1
	bytes, err := json.Marshal(globalProxies)
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("序列化globalProxies失败")
		return err
	}
	err = writeFileOrCreateIfNotExist(dataPath, bytes)
	<-globalProxiesLock
	return err
}

//----------------------------------------------------------------------------------------------------------------------

func writeFileOrCreateIfNotExist(filePath string, text []byte) error {
	_, err := os.Stat(filePath)
	if err == nil || os.IsExist(err) {
		err = ioutil.WriteFile(filePath, text, 0644)
		if err != nil {
			log.WithFields(logrus.Fields{"err": err}).Error("写入文件失败")
		}
		return err
	}
	return createFile(filePath, text)
}

func readFileOrCreateIfNotExist(filePath string, defaultText string) (string, error) {
	_, err := os.Stat(filePath)
	if err == nil || os.IsExist(err) {
		bytes, err := readFile(filePath)
		if err != nil {
			return "", err
		}
		text := string(bytes)
		log.WithFields(logrus.Fields{"text": text}).Info("读取文件文本")
		return text, err
	}
	err = createFile(filePath, []byte(defaultText))
	return defaultText, err
}

func createFile(filePath string, defaultData []byte) error {
	folderPath, _ := path.Split(filePath)
	log.WithFields(logrus.Fields{"folderPath": folderPath}).Info("文件父文件夹")
	if folderPath != "" {
		err := os.MkdirAll(folderPath, 0666)
		if err != nil {
			log.WithFields(logrus.Fields{"err": err}).Error("创建父文件夹失败")
			return err
		}
	}

	file, err := os.Create(filePath)
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("创建文件失败")
		return err
	}
	defer file.Close()
	_, err = file.Write(defaultData)
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("写入文件初始文本失败")
	}
	return err
}

func readFile(filePath string) ([]byte, error) {
	file, err := os.Open(filePath)
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("打开文件失败")
		return nil, err
	}
	defer file.Close()
	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		log.Error("读取文件失败")
		return nil, err
	}
	return bytes, err
}

//----------------------------------------------------------------------------------------------------------------------

func autoFlushProxy() {
	for {
		flushProxy()
	}
}

func flushProxy() {
	var proxies []string
	globalProxiesLock <- 1
	for i := range globalProxies {
		proxies = append(proxies, strings.Split(globalProxies[i], "//")[1])
	}
	<-globalProxiesLock
	globalProxies = []string{}
	globalProxiesMap = make(map[string]string)
	checkProxies(proxies)

	checkProxies(kuaidaili())
	checkProxies(_66ip())
	checkProxies(cnProxy())
	checkProxies(ihuan())
	checkProxies(wwwProxyList())
	checkProxies(proxyDaily())
	checkProxies(proxyFish())
	checkProxies(sslProxies())
	saveGlobalProxies()
}

func checkProxies(proxies []string) {
	for i := range proxies {
		checkProxySignal <- 1
		go checkAndAddProxy(proxies[i])
	}
}

func checkAndAddProxy(proxy string) {
	httpProxy := fmt.Sprintf("http://%s", proxy)
	log.WithFields(logrus.Fields{"httpProxy": httpProxy}).Info("代理链接")
	if checkProxy(httpProxy) {
		addProxy(httpProxy)
		<-checkProxySignal
		return
	}
	socks5Proxy := fmt.Sprintf("socks5://%s", proxy)
	log.WithFields(logrus.Fields{"socks5Proxy": socks5Proxy}).Info("代理链接")
	if checkProxy(socks5Proxy) {
		addProxy(httpProxy)
		<-checkProxySignal
		return
	}
	httpsProxy := fmt.Sprintf("https://%s", proxy)
	log.WithFields(logrus.Fields{"httpsProxy": httpsProxy}).Info("代理链接")
	if checkProxy(httpsProxy) {
		addProxy(httpProxy)
		<-checkProxySignal
		return
	}
	<-checkProxySignal
}

func checkProxy(proxy string) bool {
	request := gorequest.New().Proxy(proxy)
	response, body, errs := request.Get("https://www.baidu.com/").
		Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.77 Safari/537.36").
		Timeout(timeout).End()
	log.WithFields(logrus.Fields{"errs": errs}).Info("www.baidu.com请求")
	if errs != nil && len(errs) > 0 {
		return false
	}

	log.WithFields(logrus.Fields{"StatusCode": response.StatusCode}).Info("www.baidu.com请求")
	if response.StatusCode != 200 {
		return false
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("www.baidu.com，html解析失败")
		return false
	}
	return strings.Contains(doc.Find("title").Text(), "百度一下，你就知道")
}

//----------------------------------------------------------------------------------------------------------------------

//https://www.kuaidaili.com/free/inha/1/
func kuaidaili() []string {
	var proxyList []string
	for page := 1; page < pageMax; page++ {
		for i := 0; i < retry; i++ {
			html, err := requestKuaidaili(page)
			if err == nil {
				proxies, _ := analysisKuaidaili(html)
				if proxies == nil {
					proxies = []string{}
				}
				if len(proxies) == 0 {
					return proxyList
				}
				for i := range proxies {
					proxyList = append(proxyList, proxies[i])
				}
			}
		}
	}
	return proxyList
}

func analysisKuaidaili(html string) ([]string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("www.kuaidaili.com，html解析失败")
		return nil, err
	}

	var httpProxies []string
	tableSelection := doc.Find("table").First()
	tbodySelection := tableSelection.Find("tbody")
	tbodySelection.Find("tr").Each(func(i int, trSelection *goquery.Selection) {
		hostSelection := trSelection.Find(":nth-child(1)").First()
		portSelection := trSelection.Find(":nth-child(2)").First()
		httpProxies = append(httpProxies, fmt.Sprintf("%s:%s", hostSelection.Text(), portSelection.Text()))
	})
	return httpProxies, nil
}

func requestKuaidaili(page int) (string, error) {
	url := fmt.Sprintf("https://www.kuaidaili.com/free/inha/%d/", page)
	log.WithFields(logrus.Fields{"url": url}).Info("www.kuaidaili.com请求url")
	request := gorequest.New().Proxy(getProxy())
	response, body, errs := request.Get(url).
		Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.77 Safari/537.36").
		Timeout(timeout).End()
	log.WithFields(logrus.Fields{"errs": errs}).Info("www.kuaidaili.com请求")
	if errs != nil && len(errs) > 0 {
		return "", errors.New("www.kuaidaili.com请求异常")
	}

	log.WithFields(logrus.Fields{"StatusCode": response.StatusCode}).Info("www.kuaidaili.com请求")
	if response.StatusCode != 200 {
		return "", errors.New("www.kuaidaili.com响应码异常")
	}
	return body, nil
}

//----------------------------------------------------------------------------------------------------------------------

//http://www.66ip.cn/1.html
func _66ip() []string {
	var proxyList []string
	for page := 1; page < pageMax; page++ {
		for i := 0; i < retry; i++ {
			html, err := request66ip(page)
			if err == nil {
				proxies, _ := analysis66ip(html)
				if proxies == nil {
					proxies = []string{}
				}
				if len(proxies) == 0 {
					return proxyList
				}
				for i := range proxies {
					proxyList = append(proxyList, proxies[i])
				}
			}
		}
	}
	return proxyList
}

func analysis66ip(html string) ([]string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("www.66ip.cn，html解析失败")
		return nil, err
	}

	var httpProxies []string
	containerSelection := doc.Find("#main").First()
	tableSelection := containerSelection.Find("table").First()
	tableSelection.Find("tr").Each(func(i int, trSelection *goquery.Selection) {
		hostSelection := trSelection.Find(":nth-child(1)").First()
		portSelection := trSelection.Find(":nth-child(2)").First()
		httpProxies = append(httpProxies, fmt.Sprintf("%s:%s", hostSelection.Text(), portSelection.Text()))
	})
	return httpProxies, nil
}

func request66ip(page int) (string, error) {
	url := fmt.Sprintf("http://www.66ip.cn/%d.html", page)
	log.WithFields(logrus.Fields{"url": url}).Info("www.66ip.cn请求url")
	request := gorequest.New().Proxy(getProxy())
	response, body, errs := request.Get(url).
		Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.77 Safari/537.36").
		Timeout(timeout).End()
	log.WithFields(logrus.Fields{"errs": errs}).Info("www.66ip.cn请求")
	if errs != nil && len(errs) > 0 {
		return "", errors.New("www.66ip.cn请求异常")
	}

	log.WithFields(logrus.Fields{"StatusCode": response.StatusCode}).Info("www.66ip.cn请求")
	if response.StatusCode != 200 {
		return "", errors.New("www.66ip.cn响应码异常")
	}
	return body, nil
}

//----------------------------------------------------------------------------------------------------------------------

//http://cn-proxy.com/
func cnProxy() []string {
	for i := 0; i < retry; i++ {
		html, err := requestCnProxy()
		if err == nil {
			proxies, _ := analysisCnProxy(html)
			if proxies == nil {
				proxies = []string{}
			}
			return proxies
		}
	}
	return []string{}
}

func analysisCnProxy(html string) ([]string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("cn-proxy.com，html解析失败")
		return nil, err
	}

	var httpProxies []string
	doc.Find(".sortable").Each(func(i int, tableSelection *goquery.Selection) {
		tbodySelection := tableSelection.Find("tbody")
		tbodySelection.Find("tr").Each(func(i int, trSelection *goquery.Selection) {
			hostSelection := trSelection.Find(":nth-child(1)").First()
			portSelection := trSelection.Find(":nth-child(2)").First()
			httpProxies = append(httpProxies, fmt.Sprintf("%s:%s", hostSelection.Text(), portSelection.Text()))
		})
	})
	return httpProxies, nil
}

func requestCnProxy() (string, error) {
	request := gorequest.New().Proxy(getProxy())
	response, body, errs := request.Get("http://cn-proxy.com/").
		Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.77 Safari/537.36").
		Timeout(timeout).End()
	log.WithFields(logrus.Fields{"errs": errs}).Info("cn-proxy.com请求")
	if errs != nil && len(errs) > 0 {
		return "", errors.New("cn-proxy.com请求异常")
	}

	log.WithFields(logrus.Fields{"StatusCode": response.StatusCode}).Info("cn-proxy.com请求")
	if response.StatusCode != 200 {
		return "", errors.New("cn-proxy.com响应码异常")
	}
	return body, nil
}

//----------------------------------------------------------------------------------------------------------------------

//https://ip.ihuan.me/
func ihuan() []string {
	for i := 0; i < retry; i++ {
		html, err := requestIhuan()
		if err == nil {
			proxies, _ := analysisIhuan(html)
			if proxies == nil {
				proxies = []string{}
			}
			return proxies
		}
	}
	return []string{}
}

func analysisIhuan(html string) ([]string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("ip.ihuan.me，html解析失败")
		return nil, err
	}

	var httpProxies []string
	tableSelection := doc.Find("table").First()
	tbodySelection := tableSelection.Find("tbody")
	tbodySelection.Find("tr").Each(func(i int, trSelection *goquery.Selection) {
		hostSelection := trSelection.Find(":nth-child(1)").First()
		portSelection := trSelection.Find(":nth-child(2)").First()
		httpProxies = append(httpProxies, fmt.Sprintf("%s:%s", hostSelection.Text(), portSelection.Text()))
	})
	return httpProxies, nil
}

func requestIhuan() (string, error) {
	request := gorequest.New().Proxy(getProxy())
	response, body, errs := request.Get("https://ip.ihuan.me/").
		Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.77 Safari/537.36").
		Timeout(timeout).End()
	log.WithFields(logrus.Fields{"errs": errs}).Info("ip.ihuan.m请求")
	if errs != nil && len(errs) > 0 {
		return "", errors.New("ip.ihuan.m请求异常")
	}

	log.WithFields(logrus.Fields{"StatusCode": response.StatusCode}).Info("ip.ihuan.m请求")
	if response.StatusCode != 200 {
		return "", errors.New("ip.ihuan.m响应码异常")
	}
	return body, nil
}

//----------------------------------------------------------------------------------------------------------------------

//https://www.proxy-list.download/api/v0/get?l=en&t=http
func wwwProxyList() []string {
	for i := 0; i < retry; i++ {
		jsonString, err := requestProxyList()
		if err == nil {
			proxies, _ := analysisProxyList(jsonString)
			if proxies == nil {
				proxies = []string{}
			}
			return proxies
		}
	}
	return []string{}
}

func analysisProxyList(jsonString string) ([]string, error) {
	if !gjson.Valid(jsonString) {
		log.Error("proxy-list.download响应json非法")
		return nil, errors.New("proxy-list.download响应json非法")
	}
	result := gjson.Parse(jsonString).Array()[0].Get("LISTA")
	if !result.Exists() {
		log.Error("proxy-list.download响应json没有LISTA属性")
		return nil, errors.New("proxy-list.download响应json没有LISTA属性")
	}
	var proxyMaps []map[string]string
	err := json.Unmarshal([]byte(result.String()), &proxyMaps)
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("反序列化proxy-list.download响应json失败")
		return nil, err
	}
	var proxies []string
	for i := range proxyMaps {
		proxies = append(proxies, fmt.Sprintf("%s:%s", proxyMaps[i]["IP"], proxyMaps[i]["PORT"]))
	}
	return proxies, nil
}

func requestProxyList() (string, error) {
	request := gorequest.New().Proxy(getProxy())
	response, body, errs := request.Get(fmt.Sprintf("https://www.proxy-list.download/api/v0/get")).
		Param("l", "en").
		Param("t", "http").
		Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.77 Safari/537.36").
		Timeout(timeout).End()
	log.WithFields(logrus.Fields{"errs": errs}).Info("proxy-list.com请求")
	if errs != nil && len(errs) > 0 {
		return "", errors.New("proxy-list.com请求异常")
	}

	log.WithFields(logrus.Fields{"StatusCode": response.StatusCode}).Info("proxy-list.com请求")
	if response.StatusCode != 200 {
		return "", errors.New("proxy-list.com响应码异常")
	}
	return body, nil
}

//----------------------------------------------------------------------------------------------------------------------

//https://proxy-daily.com/
func proxyDaily() []string {
	for i := 0; i < retry; i++ {
		html, err := requestProxyDaily()
		if err == nil {
			proxies, _ := analysisProxyDaily(html)
			if proxies == nil {
				proxies = []string{}
			}
			return proxies
		}
	}
	return []string{}
}

func analysisProxyDaily(html string) ([]string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("proxy-daily.com，html解析失败")
		return nil, err
	}

	var httpProxies []string
	doc.Find(".freeproxiestyle").Each(func(i int, divSelection *goquery.Selection) {
		proxiesString := divSelection.Text()
		proxies := strings.Split(proxiesString, "\n")
		for i := range proxies {
			httpProxies = append(httpProxies, proxies[i])
		}
	})
	return httpProxies, nil
}

func requestProxyDaily() (string, error) {
	request := gorequest.New().Proxy(getProxy())
	response, body, errs := request.Get(fmt.Sprintf("https://proxy-daily.com/")).
		Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.77 Safari/537.36").
		Timeout(timeout).End()
	log.WithFields(logrus.Fields{"errs": errs}).Info("proxy-daily.com请求")
	if errs != nil && len(errs) > 0 {
		return "", errors.New("proxy-daily.com请求异常")
	}

	log.WithFields(logrus.Fields{"StatusCode": response.StatusCode}).Info("proxy-daily.com请求")
	if response.StatusCode != 200 {
		return "", errors.New("proxy-daily.com响应码异常")
	}
	return body, nil
}

//----------------------------------------------------------------------------------------------------------------------

//https://hidemy.name/en/proxy-list/?start=0
func proxyFish() []string {
	for i := 0; i < retry; i++ {
		html, err := requestProxyFish()
		if err == nil {
			proxies, _ := analysisProxyFish(html)
			if proxies == nil {
				proxies = []string{}
			}
			return proxies
		}
	}
	return []string{}
}

func analysisProxyFish(jsonString string) ([]string, error) {
	if !gjson.Valid(jsonString) {
		log.Error("proxyhttp.net响应json非法")
		return nil, errors.New("proxyhttp.net响应json非法")
	}
	result := gjson.Get(jsonString, "data")
	if !result.Exists() {
		log.Error("proxyhttp.net响应json没有data属性")
		return nil, errors.New("proxyhttp.net响应json没有data属性")
	}
	base64String := result.String()
	log.WithFields(logrus.Fields{"base64String": base64String}).Info("proxyhttp.net响应base64")
	bytes, err := base64.StdEncoding.DecodeString(base64String)
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("proxyhttp.net响应base64解码失败")
		return nil, err
	}
	proxiesJson := string(bytes)
	log.WithFields(logrus.Fields{"proxiesJson": proxiesJson}).Info("proxyhttp.net响应base64解码")
	if !gjson.Valid(proxiesJson) {
		log.Error("proxyhttp.net响应base64.json非法")
		return nil, errors.New("proxyhttp.net响应base64.json非法")
	}
	var httpProxies []string
	results := gjson.Parse(proxiesJson).Array()
	for i := range results {
		r := results[i].Array()
		httpProxies = append(httpProxies, fmt.Sprintf("%s:%s", r[1], r[2]))
	}
	return httpProxies, nil
}

func requestProxyFish() (string, error) {
	request := gorequest.New().Proxy(getProxy())
	response, body, errs := request.Get(fmt.Sprintf("https://www.proxyfish.com/proxylist/server_processing.php")).
		Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.77 Safari/537.36").
		Timeout(timeout).End()
	log.WithFields(logrus.Fields{"errs": errs}).Info("www.proxyfish.com请求")
	if errs != nil && len(errs) > 0 {
		return "", errors.New("www.proxyfish.com请求异常")
	}

	log.WithFields(logrus.Fields{"StatusCode": response.StatusCode}).Info("www.proxyfish.com请求")
	if response.StatusCode != 200 {
		return "", errors.New("www.proxyfish.com响应码异常")
	}
	return body, nil
}

//----------------------------------------------------------------------------------------------------------------------

//https://www.sslproxies.org/
func sslProxies() []string {
	for i := 0; i < retry; i++ {
		html, err := requestSslProxies()
		if err == nil {
			proxies, _ := analysisSslProxies(html)
			if proxies == nil {
				proxies = []string{}
			}
			return proxies
		}
	}
	return []string{}
}

func analysisSslProxies(html string) ([]string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		log.WithFields(logrus.Fields{"err": err}).Error("www.sslproxies.org，html解析失败")
		return nil, err
	}

	var httpProxies []string
	tableSelection := doc.Find("#proxylisttable").First()
	tbodySelection := tableSelection.Find("tbody")
	tbodySelection.Find("tr").Each(func(i int, trSelection *goquery.Selection) {
		hostSelection := trSelection.Find(":nth-child(1)").First()
		portSelection := trSelection.Find(":nth-child(2)").First()
		httpProxies = append(httpProxies, fmt.Sprintf("%s:%s", hostSelection.Text(), portSelection.Text()))
	})
	return httpProxies, nil
}

func requestSslProxies() (string, error) {
	request := gorequest.New().Proxy(getProxy())
	response, body, errs := request.Get("https://www.sslproxies.org/").
		Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/70.0.3538.77 Safari/537.36").
		Timeout(timeout).End()
	log.WithFields(logrus.Fields{"errs": errs}).Info("www.sslproxies.org请求")
	if errs != nil && len(errs) > 0 {
		return "", errors.New("www.sslproxies.org请求异常")
	}

	log.WithFields(logrus.Fields{"StatusCode": response.StatusCode}).Info("www.sslproxies.org请求")
	if response.StatusCode != 200 {
		return "", errors.New("www.sslproxies.org响应码异常")
	}
	return body, nil
}
