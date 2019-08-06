package monitor

import (
	"Project/LearnGo/Learn/webcrawler/helper/log"
	"Project/LearnGo/Learn/webcrawler/scheduler"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"time"
)

// 日志记录器
var logger = log.DLogger()

// 监控结果摘要结构
type summary struct {
	// goroutine 数量
	NumGoroutine int `json:"num_goroutine"`
	// 调度器的摘要信息
	SchedSummary scheduler.SummaryStruct `json:"sched_summary"`
	// 监控时间
	EscapedTime string `json:"escaped_time"`
}

// msgReachMaxIdleCount 代表已达到最大空闲计数的消息模板。
var msgReachMaxIdleCount = "The scheduler has been idle for a period of time" +
	" (about %s)." + " Consider to stop it now."

// msgStopScheduler 代表停止调度器的消息模板。
var msgStopScheduler = "Stop scheduler...%s."

// Record 代表日志记录函数的类型
// 参数level代表日志级别，0：普通，1：警告，2：错误
type Record func(level uint8, content string)

// Monitor 用于监控调度器。
// 参数scheduler代表作为监控目标的调度器。
// 参数checkInterval代表检查间隔时间，单位：纳秒。
// 参数summarizeInterval代表摘要获取间隔时间，单位：纳秒。
// 参数maxIdleCount代表最大空闲计数。
// 参数autoStop被用来指示该方法是否在调度器空闲足够长的时间之后自行停止调度器。
// 参数record代表日志记录函数。
// 当监控结束之后，该方法会向作为唯一结果值的通道发送一个代表了空闲状态检查次数的数值。
func Monitor(scheduler scheduler.Scheduler, checkInterval time.Duration, summarizeInterval time.Duration, maxIdleCount uint, autoStop bool, record Record) <-chan uint64 {
	// 防止调度器不可用
	if scheduler == nil {
		panic(errors.New("The Scheduler is invalid"))
	}

	// 防止过小的检查时间占用系统资源
	if checkInterval < time.Millisecond*100 {
		checkInterval = time.Millisecond * 100
	}

	// 防止过小的摘要获取时间占用系统资源
	if summarizeInterval < time.Second {
		summarizeInterval = time.Second
	}

	// 防止过小的最大空闲计数造成调度器的过早停止
	if maxIdleCount < 10 {
		maxIdleCount = 10
	}

	logger.Infof("Monitor parameters: checkInterval: %s, summarizeInterval: %s,"+
		" maxIdleCount: %d, autoStop: %v",
		checkInterval, summarizeInterval, maxIdleCount, autoStop)

	// 生成监控停止通知器
	stopNotifier, stopFunc := context.WithCancel(context.Background())
	// 接收和报告错误
	reportError(scheduler, record, stopNotifier)
	// 记录摘要信息
	recordSummary(scheduler, summarizeInterval, record, stopNotifier)
	// 检查计数通道
	checkCountChan := make(chan uint64, 2)
	// 检查空闲状态
	checkStatus(scheduler, checkInterval, maxIdleCount, autoStop, checkCountChan, record, stopFunc)

	return checkCountChan
}

func checkStatus(scheduler scheduler.Scheduler, checkInterval time.Duration, maxIdleCount uint, autoStop bool,
	checkCountChan chan<- uint64, record Record, stopFunc context.CancelFunc) {
	go func() {
		var checkCount uint64
		defer func() {
			stopFunc()
			checkCountChan <- checkCount
		}()
		// 等待调度器开启。
		waitForSchedulerStart(scheduler)
		// 准备。
		var idleCount uint
		var firstIdleTime time.Time
		for {
			// 检查调度器的空闲状态。
			if scheduler.Idle() {
				idleCount++
				if idleCount == 1 {
					firstIdleTime = time.Now()
				}
				if idleCount >= maxIdleCount {
					msg :=
						fmt.Sprintf(msgReachMaxIdleCount, time.Since(firstIdleTime).String())
					record(0, msg)
					// 再次检查调度器的空闲状态，确保它已经可以被停止。
					if scheduler.Idle() {
						if autoStop {
							var result string
							if err := scheduler.Stop(); err == nil {
								result = "success"
							} else {
								result = fmt.Sprintf("failing(%s)", err)
							}
							msg = fmt.Sprintf(msgStopScheduler, result)
							record(0, msg)
						}
						break
					} else {
						if idleCount > 0 {
							idleCount = 0
						}
					}
				}
			} else {
				if idleCount > 0 {
					idleCount = 0
				}
			}
			checkCount++
			time.Sleep(checkInterval)
		}
	}()
}

func waitForSchedulerStart(sl scheduler.Scheduler) {
	for sl.Status() != scheduler.SCHED_STATUS_STARTED {
		time.Sleep(time.Microsecond)
	}
}

func recordSummary(sl scheduler.Scheduler, summarizeInterval time.Duration, record Record, stopNotifier context.Context) {
	go func() {
		// 等待调度器开启。
		waitForSchedulerStart(sl)
		// 准备。
		var prevSchedSummaryStruct scheduler.SummaryStruct
		var prevNumGoroutine int
		var recordCount uint64 = 1
		startTime := time.Now()
		for {
			// 检查监控停止通知器。
			select {
			case <-stopNotifier.Done():
				return
			default:
			}
			// 获取Goroutine数量和调度器摘要信息。
			currNumGoroutine := runtime.NumGoroutine()
			currSchedSummaryStruct := sl.Summary().Struct()
			// 比对前后两份摘要信息的一致性。只有不一致时才会记录。
			if currNumGoroutine != prevNumGoroutine ||
				!currSchedSummaryStruct.Same(prevSchedSummaryStruct) {
				// 记录摘要信息。
				summay := summary{
					NumGoroutine: runtime.NumGoroutine(),
					SchedSummary: currSchedSummaryStruct,
					EscapedTime:  time.Since(startTime).String(),
				}
				b, err := json.MarshalIndent(summay, "", "    ")
				if err != nil {
					logger.Errorf("An error occurs when generating scheduler summary: %s\n", err)
					continue
				}
				msg := fmt.Sprintf("Monitor summary[%d]:\n%s", recordCount, b)
				record(0, msg)
				prevNumGoroutine = currNumGoroutine
				prevSchedSummaryStruct = currSchedSummaryStruct
				recordCount++
			}
			time.Sleep(summarizeInterval)
		}
	}()
}

func reportError(sl scheduler.Scheduler, record Record, stopNotifier context.Context) {
	go func() {
		// 等待调度器开启。
		waitForSchedulerStart(sl)
		errorChan := sl.ErrorChan()
		for {
			// 查看监控停止通知器。
			select {
			case <-stopNotifier.Done():
				return
			default:
			}
			err, ok := <-errorChan
			if ok {
				errMsg := fmt.Sprintf("Received an error from error channel: %s", err)
				record(2, errMsg)
			}
			time.Sleep(time.Microsecond)
		}
	}()
}
