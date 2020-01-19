package h_kafkaproducer

import (
	context2 "context"
	"errors"
	"fmt"
	"github.com/Shopify/sarama"
	"reflect"
	"strconv"
	"strings"
	"sunset/data-stream/context"
	"sunset/data-stream/logger"
	"sunset/data-stream/model/kafka"
	"sunset/data-stream/stream"
	"sunset/data-stream/util"
	"sync"
	"time"
)

func init() {
	stream.RegistHandler("kafkaproducer", &KafkaProducer{})
}

type KafkaProducer struct {
	sync.Mutex
	Bootstrap     []string
	retry         int
	kafkaProducer sarama.SyncProducer

	ctx    context2.Context
	cancel context2.CancelFunc
}

func (p *KafkaProducer) Init(config interface{}) error {
	retry := context.GetInt("kafkaproducer_retry")
	if !context.IsExsit("kafkaproducer_retry") {
		retry = -1
	}
	logger.LOG_INFO("启动 kafka-producer")
	logger.LOG_WARN("---------------- kafkaproducer config ----------------")
	logger.LOG_WARN("kafkaproducer_bootstrap : " + context.GetString("kafkaproducer_bootstrap"))
	logger.LOG_WARN("kafkaproducer_retry : " + strconv.Itoa(retry))
	logger.LOG_WARN("------------------------------------------------------")
	unConfigKeys := context.Exsit("kafkaproducer_bootstrap")
	if len(unConfigKeys) > 0 {
		return errors.New("缺少配置：" + strings.Join(unConfigKeys, ","))
	}
	p.ctx, p.cancel = context2.WithCancel(context2.Background())
	p.Bootstrap = strings.Split(strings.Trim(context.GetString("kafkaproducer_bootstrap"), " "), ",")
	p.retry = retry
	go p.InitConnection()
	return nil
}

func (p *KafkaProducer) InitConnection() {
	p.Lock()
	if p.kafkaProducer != nil {
		_ = p.kafkaProducer.Close()
	}
	config := sarama.NewConfig()
	//config.Version = sarama.V0_10_1_0
	config.Version = sarama.V2_3_0_0
	config.Producer.Return.Successes = true
	syncProducer, err := sarama.NewSyncProducer(p.Bootstrap, config)
	p.kafkaProducer = syncProducer
	if err != nil {
		logger.LOG_ERROR("创建同步kafka-producer失败", err)
		p.kafkaProducer = nil
	}
	p.Unlock()
}

func (p *KafkaProducer) Handle(data interface{}, next func(interface{}) error) error {
	msgs, ok := data.([]*kafka.KafkaMessage)
	if !ok {
		return errors.New(fmt.Sprintf("Handle [KafkaMessage] 数据格式错误，need []*kafka.KafkaMessage , get %T", reflect.TypeOf(data)))
	}
	if len(msgs) == 0 {
		return nil
	}
	kafkamsgs := Cast(msgs)
	err := util.RetryCancelWithContext(func() error {
		p.Lock()
		kafkaProducer := p.kafkaProducer
		p.Unlock()
		if kafkaProducer == nil {
			//发送异常、重连
			p.InitConnection()
			return errors.New("kafka producer 未连接")
		}
		err := kafkaProducer.SendMessages(kafkamsgs)
		if err != nil {
			//发送异常、重连
			p.Lock()
			p.kafkaProducer = nil
			p.Unlock()
		}
		return err
	}, p.retry, 1*time.Second, p.ctx)
	logger.LOG_INFO("kafkaproducer send msgs：%d", len(kafkamsgs))
	return err
}

func Cast(msgs []*kafka.KafkaMessage) []*sarama.ProducerMessage {
	kafkaMsgs := make([]*sarama.ProducerMessage, 0)
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		if msg.Value == nil {
			msg.Value = make([]byte, 0)
		}
		bytes := sarama.ByteEncoder(msg.Value)
		kafkaMsgs = append(kafkaMsgs, &sarama.ProducerMessage{
			Topic: msg.Topic,
			Key:   nil,
			Value: bytes,
			Headers: func() []sarama.RecordHeader {
				var rhs []sarama.RecordHeader
				if len(msg.Headers) > 0 {
					for _, h := range msg.Headers {
						if h == nil {
							continue
						}
						if h.Key == nil {
							continue
						}
						rhs = append(rhs, sarama.RecordHeader{
							Key:   h.Key,
							Value: h.Value,
						})
					}
				}
				logger.LOG_INFO("header长度：", len(msg.Headers), len(msg.Headers))
				return rhs
			}(),
			//Metadata:  nil,
			//Offset:    0,
			//Partition: 0,
			//Timestamp: time.Time{},
		})
		logger.LOG_DEBUG("单条消息大小：%d", len(bytes))
	}
	return kafkaMsgs
}

func (p *KafkaProducer) Close() error {
	if p.cancel != nil {
		p.cancel()
	}
	if p.kafkaProducer != nil {
		err := p.kafkaProducer.Close()
		if err != nil {
			logger.LOG_WARN("关闭kafka生产者异常", err)
		}
	}
	return nil
}
