package context

import (
	"errors"
	"github.com/spf13/viper"
	"sunset/data-stream/model"
	"sync/atomic"
	"time"
)

const (
	PARAM_KEY_NODE_ID      = "NODE_ID"
	PARAM_KEY_COMPONENT_ID = "COMPONENT_ID"
	PARAM_KEY_NODE_ADDR    = "NODE_ADDR"
)

const CONFIG_RESOURCE_REFRESH_DELAY = 1 //1秒延迟触发变更

//配置
func Set(key string, value interface{}) {
	viper.Set(key, value)
	delayEmitRefreshConfig(func() {
		if len(configWatchers) > 0 {
			for _, cb := range configWatchers {
				cb()
			}
		}
	})
}

func Exsit(keys ...string) (unExsitKeys []string) {
	unExsitKeys = make([]string, 0)
	for _, k := range keys {
		v := viper.IsSet(k)
		if !v {
			unExsitKeys = append(unExsitKeys, k)
		}
	}
	return unExsitKeys
}

func IsExsit(keys ...string) bool {
	return len(Exsit(keys...)) == 0
}

func GetTask() (*model.Task, error) {
	task, ok := viper.Get("$task").(*model.Task)
	if task == nil || !ok {
		return nil, errors.New("无任务")
	}
	return task, nil
}
func GetString(key string) string {
	return viper.GetString(key)
}
func GetInt(key string) int {
	return viper.GetInt(key)
}
func GetInt32(key string) int32 {
	return viper.GetInt32(key)
}
func GetInt64(key string) int64 {
	return viper.GetInt64(key)
}
func GetBool(key string) bool {
	return viper.GetBool(key)
}

func AssignConfig(cfgs []map[string]interface{}) {
	if len(cfgs) > 0 {
		for _, m := range cfgs {
			if len(m) > 0 {
				for k, v := range m {
					Set(k, v)
				}
			}
		}
	}
}

//资源
//设备
var RESOURCE_ALL = make([]*model.Resource, 0)
var RESOURCE_ALL_INDEX = make(map[string]int)
var RESOURCE_ID_EQ = make(map[string]*model.Resource)
var RESOURCE_GBID_EQ = make(map[string]*model.Resource)

func AssignResources(eqs []*model.Resource) {
	if len(eqs) > 0 {
		for _, eq := range eqs {
			if index, ok := RESOURCE_ALL_INDEX[eq.ID]; ok {
				RESOURCE_ALL[index] = eq
			} else {
				RESOURCE_ALL = append(RESOURCE_ALL, eq)
				RESOURCE_ALL_INDEX[eq.ID] = len(RESOURCE_ALL) - 1
			}
			RESOURCE_ID_EQ[eq.ID] = eq
			RESOURCE_ID_EQ[eq.GbID] = eq
		}
		refreshResource()
	}
}

func RevokeResources(eqs []*model.Resource) {
	if len(eqs) > 0 {
		for _, eq := range eqs {
			if index, ok := RESOURCE_ALL_INDEX[eq.ID]; ok {
				RESOURCE_ALL = append(RESOURCE_ALL[:index], RESOURCE_ALL[:index+1]...)
				delete(RESOURCE_ALL_INDEX, eq.ID)
				delete(RESOURCE_ID_EQ, eq.ID)
				delete(RESOURCE_GBID_EQ, eq.GbID)
			}
		}
		refreshResource()
	}
}

func refreshResource() {
	delayEmitRefreshResource(func() {
		if len(resourceWatchers) > 0 {
			for _, cb := range resourceWatchers {
				cb()
			}
		}
	})
}

func ExsitResource(id string) bool {
	return ExsitGbId(id) || ExsitResourceId(id)
}

func ExsitGbId(gbId string) bool {
	_, isExsit := RESOURCE_GBID_EQ[gbId]
	return isExsit
}
func ExsitResourceId(resourceId string) bool {
	_, isExsit := RESOURCE_ID_EQ[resourceId]
	return isExsit
}

//监听

var configWatchers = make([]func(), 0)
var resourceWatchers = make([]func(), 0)

func WatchConfig(cb func()) {
	configWatchers = append(configWatchers, cb)
}
func WatchResource(cb func()) {
	resourceWatchers = append(resourceWatchers, cb)
}

var configRefreshIndex int32

func delayEmitRefreshConfig(cb func()) {
	atomic.AddInt32(&configRefreshIndex, 1)
	go func(index int32) {
		<-time.NewTimer(CONFIG_RESOURCE_REFRESH_DELAY * time.Second).C
		currentIndex := atomic.LoadInt32(&configRefreshIndex)
		if index != currentIndex {
			return
		}
		atomic.StoreInt32(&configRefreshIndex, 0)
		cb()
	}(configRefreshIndex)
}

var resourceRefreshIndex int32

func delayEmitRefreshResource(cb func()) {
	atomic.AddInt32(&resourceRefreshIndex, 1)
	go func(index int32) {
		<-time.NewTimer(CONFIG_RESOURCE_REFRESH_DELAY * time.Second).C
		currentIndex := atomic.LoadInt32(&resourceRefreshIndex)
		if index != currentIndex {
			return
		}
		atomic.StoreInt32(&resourceRefreshIndex, 0)
		cb()
	}(resourceRefreshIndex)
}
