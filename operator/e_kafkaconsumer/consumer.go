package e_kafkaconsumer

import (
	"context"
	"errors"
	"github.com/Shopify/sarama"
	cluster "github.com/bsm/sarama-cluster"
	"strconv"
	"strings"
	"sunset/data-stream/concurrent"
	dagContext "sunset/data-stream/context"
	"sunset/data-stream/logger"
	"sunset/data-stream/model/kafka"
	"sunset/data-stream/stream"
	"sync"
	"time"
)

func init() {
	stream.RegistEmitter("kafkaconsumer", &KafkaConsumer{})
}

type FromOffset int

const (
	FROM_OFFSET_NEWEST FromOffset = 0
	FROM_OFFSET_OLDEST FromOffset = 1
)

type KafkaConsumer struct {
	sync.Mutex

	Bootstrap []string
	Topics    []string
	GroupId   string
	FromOffset
	BatchSize     int
	BatchDelay    int
	parallelSize int
	kafkaConsumer *cluster.Consumer
	emit          func(interface{}) error
	executor      *concurrent.Executor
	cancelCtx     context.Context
	cancel        func()
}

func (consumer *KafkaConsumer) Init(emit func(interface{}) error) error {
	logger.LOG_INFO("启动kafka-consumer")
	_ = consumer.Close()
	consumer.emit = emit
	logger.LOG_WARN("---------------- kafkaconsumer config ----------------")
	logger.LOG_WARN("kafkaconsumer_bootstrap : " + dagContext.GetString("kafkaconsumer_bootstrap"))
	logger.LOG_WARN("kafkaconsumer_topics : " + dagContext.GetString("kafkaconsumer_topics"))
	logger.LOG_WARN("kafkaconsumer_groupId : " + dagContext.GetString("kafkaconsumer_groupId"))
	logger.LOG_WARN("kafkaconsumer_fromEarliestOffset : " + strconv.FormatBool(dagContext.GetBool("kafkaconsumer_fromEarliestOffset")))
	logger.LOG_WARN("kafkaconsumer_batchSize : " + strconv.Itoa(dagContext.GetInt("kafkaconsumer_batchSize")))
	logger.LOG_WARN("kafkaconsumer_batchDelay : " + strconv.Itoa(dagContext.GetInt("kafkaconsumer_batchDelay")))
	logger.LOG_WARN("kafkaconsumer_parallel : " + strconv.Itoa(dagContext.GetInt("kafkaconsumer_parallel")))
	logger.LOG_WARN("kafkaconsumer_parallelSize : " + strconv.Itoa(dagContext.GetInt("kafkaconsumer_parallelSize")))
	logger.LOG_WARN("------------------------------------------------------")
	unConfigKeys := dagContext.Exsit("kafkaconsumer_bootstrap", "kafkaconsumer_topics", "kafkaconsumer_groupId")
	if len(unConfigKeys) > 0 {
		return errors.New("缺少配置：" + strings.Join(unConfigKeys, ","))
	}
	consumer.Bootstrap = strings.Split(strings.Trim(dagContext.GetString("kafkaconsumer_bootstrap"), " "), ",")
	consumer.Topics = strings.Split(strings.Trim(dagContext.GetString("kafkaconsumer_topics"), " "), ",")
	consumer.GroupId = dagContext.GetString("kafkaconsumer_groupId")
	consumer.FromOffset = FROM_OFFSET_NEWEST
	if dagContext.GetBool("kafkaconsumer_fromEarliestOffset") {
		consumer.FromOffset = FROM_OFFSET_OLDEST
	}
	consumer.BatchSize = dagContext.GetInt("kafkaconsumer_batchSize")
	if consumer.BatchSize < 1 {
		consumer.BatchSize = 1
	}
	consumer.BatchDelay = dagContext.GetInt("kafkaconsumer_batchDelay")

	parallel := dagContext.GetInt("kafkaconsumer_parallel")
	parallelSize := dagContext.GetInt("kafkaconsumer_parallelSize")
	if parallel <= 0 {
		parallel = consumer.BatchSize
	}
	if parallelSize <= 0 {
		parallelSize = 1
	}
	consumer.executor = concurrent.NewExecutor(parallel)
	consumer.parallelSize = parallelSize
	go consumer.Start()
	return nil
}

func (c *KafkaConsumer) Start() {
	cancelCtx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	c.cancelCtx = cancelCtx

	config := cluster.NewConfig()
	config.Consumer.Return.Errors = true
	config.Group.Return.Notifications = true
	config.Version = sarama.V2_3_0_0
	config.Net.DialTimeout = 5 * time.Second
	if c.FromOffset == FROM_OFFSET_NEWEST {
		config.Consumer.Offsets.Initial = sarama.OffsetNewest
	} else {
		config.Consumer.Offsets.Initial = sarama.OffsetOldest
	}

OUT_LOOP:
	for {
		select {
		case <-cancelCtx.Done():
			break OUT_LOOP
		default:
			consumer, err := cluster.NewConsumer(c.Bootstrap, c.GroupId, c.Topics, config)
			if err == nil {
				c.kafkaConsumer = consumer
				go c.handleErrors()
				go c.handleNotifications()
				c.consumeAndSend()
				break
			} else {
				logger.LOG_ERROR("创建DAG-HUB kafka consumer 失败，重新连接：", err)
			}
		}
	}
}

func (c *KafkaConsumer) handleErrors() {
OUT_LOOP:
	for {
		select {
		case err, ok := <-c.kafkaConsumer.Errors():
			if ok {
				logger.LOG_WARN("kafka消费异常", err)
			} else {
				break OUT_LOOP
			}
		case <-c.cancelCtx.Done():
			break OUT_LOOP
		}
	}
}

func (c *KafkaConsumer) handleNotifications() {
OUT_LOOP:
	for {
		select {
		case ntf, ok := <-c.kafkaConsumer.Notifications():
			if ok {
				logger.LOG_WARN(ntf, nil)
			} else {
				break OUT_LOOP
			}
		case <-c.cancelCtx.Done():
			break OUT_LOOP
		}
	}
}

func (c *KafkaConsumer) consumeAndSend() {
	if c.BatchSize == 1 {
		//single
	OUT_LOOP:
		for {
			select {
			case msg, ok := <-c.kafkaConsumer.Messages():
				if ok {
					logger.LOG_INFO("kafkaconsumer receive msg")
					_ = c.emit([]*kafka.KafkaMessage{castKafkaMessage(msg)})
					c.kafkaConsumer.MarkOffset(msg, "")
					c.kafkaConsumer.CommitOffsets()
				} else {
					logger.LOG_WARN("kafka消费失败", nil)
					break OUT_LOOP
				}
			case <-c.cancelCtx.Done():
				break OUT_LOOP
			}
		}
	} else {
		//batch
		var batchDelay time.Duration = 1 * time.Millisecond
		if c.BatchDelay > 0 {
			batchDelay = time.Duration(c.BatchDelay) * time.Millisecond
		}
		idleDelay := time.NewTimer(batchDelay)
		msgs := make([]*kafka.KafkaMessage, c.BatchSize)
		kafkaMsgs := make([]*sarama.ConsumerMessage, c.BatchSize)
	OUT_LOOP2:
		for {
			kafkaMsgs = kafkaMsgs[:0]
			msgs = msgs[:0]
			if !idleDelay.Stop() {
				select {
				case <-idleDelay.C: //try to drain from the channel
				default:
				}
			}
			idleDelay.Reset(batchDelay)
		IN_LOOP:
			for i := 0; i < c.BatchSize; i++ {
				select {
				case msg, ok := <-c.kafkaConsumer.Messages():
					if ok {
						kafkaMsgs = append(kafkaMsgs, msg)
						msgs = append(msgs, castKafkaMessage(msg))
					} else {
						logger.LOG_WARN("kafka消费失败", nil)
						break OUT_LOOP2
					}
				case <-idleDelay.C:
					break IN_LOOP
				case <-c.cancelCtx.Done():
					break OUT_LOOP2
				}
			}
			if len(msgs) == 0 {
				continue
			}
			//emit
			logger.LOG_INFO("kafkaconsumer receive msgs : ", len(msgs))

			//_ = c.emit(msgs)
			tasks := make([]func(), 0)
			rebatchKafkaMessage(len(msgs), c.parallelSize, func(start, end int) {
				func(m []*kafka.KafkaMessage) {
					tasks = append(tasks, func() {
						_ = c.emit(m)
					})
				}(msgs[start:end])
			})
			_ = c.executor.SubmitSyncBatch(tasks)
			var kafkaConsumer *cluster.Consumer
			c.Lock()
			kafkaConsumer = c.kafkaConsumer
			c.Unlock()

			if kafkaConsumer == nil {
				return
			}
			for index, _ := range msgs {
				kafkaConsumer.MarkOffset(kafkaMsgs[index], "")
			}
			for {
				err := kafkaConsumer.CommitOffsets()
				if err == nil {
					break
				}
				logger.LOG_WARN("提交kafka offset异常，重试", err)
				time.Sleep(1 * time.Second)
			}
		}
	}
}

func rebatchKafkaMessage(count int, batchSize int, cb func(start, end int)) {
	start := 0
	for start < count {
		end := start + batchSize
		if end > count {
			end = count
		}
		cb(start, end)
		start += batchSize
	}
}

func castKafkaMessage(msg *sarama.ConsumerMessage) *kafka.KafkaMessage {
	return &kafka.KafkaMessage{
		Key:       msg.Key,
		Value:     msg.Value,
		Topic:     msg.Topic,
		Partition: msg.Partition,
		Offset:    msg.Offset,
		Headers: func() []*kafka.MessageHeader {
			var hs []*kafka.MessageHeader
			if len(msg.Headers) > 0 {
				for _, h := range msg.Headers {
					hs = append(hs, &kafka.MessageHeader{
						Key:   h.Key,
						Value: h.Value,
					})
				}
			}
			return hs
		}(),
		Timestamp:      msg.Timestamp,
		BlockTimestamp: msg.BlockTimestamp,
	}
}

func (c *KafkaConsumer) Close() error {
	if c.kafkaConsumer != nil {
		err := c.kafkaConsumer.Close()
		if err != nil {
			logger.LOG_WARN("关闭kafka消费者异常", err)
			return err
		}
	}
	if c.executor != nil {
		c.executor.Close()
	}
	if c.cancel != nil {
		c.cancel()
	}
	return nil
}
