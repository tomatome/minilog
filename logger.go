// logger

//Go support for leveled logs
//
// Copyright 2016 @wren. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//It provides functions Ltrace, Ldebug, Linfo, Lwarn, Lerror, Lfatal
//Basic examples:

//    l := InitLogger()
//    defer CloseLogger()
//    l.SetLogLevel("trace")
//    l.SetLogMode(ToStderr)
//    l.SetLogDir("/tmp")
//    Ltrace("hello world")
//    Lerror("this is a test")

// It has two rolling policy: file max size and daily on zero
// examples:

//    l := InitLogger()
//    defer CloseLogger()
//    l.SetMaxFileNum(2)   // default daily on zero
//    l.SetFileMaxSize(4)  // use file max size policy to replace file max size policy
//

package minilog

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

var logger *Logger

type severity int

const (
	traceLevel severity = iota
	debugLevel
	infoLevel
	warnLevel
	errorLevel
	fatalLevel
	numSeverity = 6
)

var severityName = []string{
	traceLevel: "TRACE",
	debugLevel: "DEBUG",
	infoLevel:  "INFO ",
	warnLevel:  "WARN ",
	errorLevel: "ERROR",
	fatalLevel: "FATAL",
}

type Mode int

const (
	ToFile       = (1 << 0)
	ToStderr     = (1 << 1)
	AlsoToStderr = (ToFile | ToStderr)
)

func (m Mode) String() string {
	switch m {
	case ToFile:
		return "log to file"
	case ToStderr:
		return "print to stderr"
	case AlsoToStderr:
		return "log to file and print to stderr"
	default:
		return "unknow log mode"
	}
}

type Logger struct {
	maxSize    int
	maxFileNum int
	level      severity
	logDir     string
	logMode    Mode
	logName    string
	prevLog    *lastLog
	keepName   []string
	createTime int64
	callHeader func(string) string
	buffer     *logBuffer
	mu         sync.Mutex
	writer     flushWriter
	nBytes     int
}

type lastLog struct {
	repeatLog  string
	repeatNum  int
	lastHeader string
}

var (
	pid      = os.Getpid()
	program  = filepath.Base(os.Args[0])
	host     = "unknownhost"
	userName = "unknownuser"
)

func init() {
	h, err := os.Hostname()
	if err == nil {
		host = shortHostname(h)
	}

	current, err := user.Current()
	if err == nil {
		userName = current.Username
	}

	// Sanitize userName since it may contain filepath separators on Windows.
	userName = strings.Replace(userName, `\`, "_", -1)
}

func shortHostname(hostname string) string {
	if i := strings.Index(hostname, "."); i >= 0 {
		return hostname[:i]
	}
	return hostname
}

func severityByName(s string) severity {
	s = strings.ToUpper(s)
	for i, name := range severityName {
		if name == s {
			return severity(i)
		}
	}
	return warnLevel
}

// init logger for log
func InitLogger() *Logger {
	l := new(Logger)
	l.maxSize = 0
	l.maxFileNum = 1
	l.level = warnLevel
	l.logDir = os.TempDir()
	l.logMode = 0
	l.prevLog = new(lastLog)
	l.logName = program + ".log." + host
	l.SetLogHeader(l.formatHeader)
	l.nBytes = 0
	logger = l

	return l
}
func CloseLogger() {
	logger.close()
}

// print logger config
func (l *Logger) PrintLogger() {
	fmt.Printf("logDir<%s>, logName<%s>, level<%s>, maxSize<%d kb>, maxFileNum<%d>, logMode=%s\n",
		l.logDir, l.logName, severityName[l.level],
		l.maxSize/1024, l.maxFileNum,
		l.logMode)
}

// file max size
func (l *Logger) SetFileMaxSize(maxSize int) {
	if maxSize < 0 {
		return
	}
	l.maxSize = maxSize * 1024
}
func (l *Logger) getFileMaxSize() int {
	return l.maxSize
}

// max file num
func (l *Logger) SetMaxFileNum(maxFileNum int) {
	if maxFileNum < 0 {
		return
	}

	l.maxFileNum = maxFileNum
}
func (l *Logger) getMaxFileNum() int {
	return l.maxFileNum
}

// log level
func (l *Logger) SetLogLevel(level string) {
	l.level = severityByName(level)
}
func (l *Logger) getLogLevel() severity {
	return l.level
}
func (l *Logger) IsLogFatal() bool {
	if l.level == fatalLevel {
		return true
	}
	return false
}
func (l *Logger) IsLogError() bool {
	if l.level == errorLevel {
		return true
	}
	return false
}
func (l *Logger) IsLogWarn() bool {
	if l.level == warnLevel {
		return true
	}
	return false
}
func (l *Logger) IsLogInfo() bool {
	if l.level == infoLevel {
		return true
	}
	return false
}
func (l *Logger) IsLogDebug() bool {
	if l.level == debugLevel {
		return true
	}
	return false
}
func (l *Logger) IsLogTrace() bool {
	if l.level == traceLevel {
		return true
	}
	return false
}

// log dir
func (l *Logger) SetLogDir(logDir string) {
	err := os.Mkdir(logDir, 0666)
	if err != nil && os.IsNotExist(err) {
		return
	}
	l.logDir = logDir
}
func (l *Logger) getLogDir() string {
	return l.logDir
}

// log mode
func (l *Logger) SetLogMode(logMode Mode) {
	l.logMode = logMode
}
func (l *Logger) getLogMode() Mode {
	return l.logMode
}
func (l *Logger) isLogFileMode() bool {
	if (l.logMode & ToFile) != 0 {

		return true
	}
	return false
}
func (l *Logger) isLogStderrMode() bool {
	if (l.logMode & ToStderr) != 0 {
		return true
	}
	return false
}

// userdef log header
func (l *Logger) SetLogHeader(header func(string) string) {
	l.callHeader = header
}
func (l *Logger) getLogHeader() func(string) string {
	return l.callHeader
}
func (l *Logger) SetLogConfig(logDir, level string, maxSize, maxFileNum int, logMode Mode) {
	l.SetLogDir(logDir)
	l.SetLogLevel(level)
	l.SetFileMaxSize(maxSize)
	l.SetMaxFileNum(maxFileNum)
	l.logMode = logMode
}

// get buffer
func (l *Logger) getBuffer() *logBuffer {
	b := l.buffer
	if b != nil {
		l.buffer = b.next
	}

	if b == nil {
		b = new(logBuffer)
	} else {
		b.next = nil
		b.Reset()
	}

	return b
}

type flushWriter interface {
	Flush() error
	Sync() error
	Fclose() error
	io.Writer
}

type syncBuffer struct {
	*bufio.Writer
	file *os.File
}

func (sbuf *syncBuffer) Sync() error {
	return sbuf.file.Sync()
}
func (sbuf *syncBuffer) Fclose() error {
	return sbuf.file.Close()
}

type logBuffer struct {
	bytes.Buffer
	next *logBuffer
}

// get log filename, funcname and line number
func GetLogFileLine(depth int) (string, string, int) {
	var funcName string
	pc, file, line, ok := runtime.Caller(3 + depth)
	if !ok {
		funcName = "unknow"
		file = "???"
		line = 1
	} else {
		idx := strings.LastIndex(file, "/")
		if idx >= 0 {
			file = file[idx+1:]
		}
		fullName := runtime.FuncForPC(pc).Name()
		shortName := strings.Split(fullName, ".")
		funcName = shortName[len(shortName)-1]
	}

	return funcName, file, line
}

// log header by default
func (l *Logger) formatHeader(level string) string {
	now := time.Now()
	year, month, day := now.Date()
	hour, minute, second := now.Clock()
	usec := now.Nanosecond() / 1000000

	_, file, line := GetLogFileLine(2)
	// yy-mm-dd hh:mm:ss.uuuu level pid file[line]:
	header := fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d.%04d [%s] %d %s[%d]",
		year, month, day, hour, minute, second, usec,
		level, os.Getpid(), file, line)

	return header
}

// Trace
func Ltrace(format string, args ...interface{}) {
	logger.println(traceLevel, format, args...)
}

// Debug
func Ldebug(format string, args ...interface{}) {
	logger.println(debugLevel, format, args...)
}

// Info
func Linfo(format string, args ...interface{}) {
	logger.println(infoLevel, format, args...)
}

// Warning
func Lwarn(format string, args ...interface{}) {
	logger.println(warnLevel, format, args...)
}

// Error
func Lerror(format string, args ...interface{}) {
	logger.println(errorLevel, format, args...)
}

// Fatal
func Lfatal(format string, args ...interface{}) {
	logger.println(fatalLevel, format, args...)
}

func (l *Logger) println(s severity, format string, args ...interface{}) {
	if l.level > s && s < numSeverity {
		return
	}

	header := l.callHeader(severityName[s])
	message := fmt.Sprintf(format, args...)

	// equal prev log message
	if l.prevLog.repeatLog == message {
		l.prevLog.repeatNum++
		l.prevLog.lastHeader = header
		return
	} else {
		l.printLastLog()
		l.prevLog.repeatLog = message
		l.prevLog.lastHeader = header
		l.prevLog.repeatNum = 0
	}

	l.output(header, message)
}

// rename file when rolling policy
func rename(fname, kname string) (string, int) {
	var n int = 0
	lenk := len(kname)
	lenf := len(fname)
	if lenk > lenf {
		idx := kname[(lenf + 1):]
		n, _ = strconv.Atoi(idx)
	}

	name := fmt.Sprintf("%s.%d", fname, n+1)
	err := os.Rename(kname, name)
	if err != nil {
		fmt.Println(err)
	}
	return name, n + 1
}

// create log file by default
func (l *Logger) createLogFile() *syncBuffer {
	sBuf := new(syncBuffer)

	fname := filepath.Join(l.logDir, l.logName)

	//
	if l.keepName == nil {
		l.keepName = make([]string, l.maxFileNum)
	}
	length := len(l.keepName)
	for i := length - 1; i >= 0; i-- {
		if len(l.keepName[i]) == 0 {
			continue
		}
		if i == l.maxFileNum-1 {
			os.Remove(l.keepName[i])
			continue
		}
		if i == 0 {
			l.writer.Fclose()
		}
		name, n := rename(fname, l.keepName[i])
		l.keepName[n] = name
	}

	f, err := os.Create(fname)
	if err != nil {
		fmt.Printf("can't create log file<%s>\n", fname)
		os.Exit(-1)
	}
	sBuf.file = f
	sBuf.Writer = bufio.NewWriterSize(f, 1024*1024)
	l.createTime = getCreateTime()
	l.keepName[0] = fname

	return sBuf
}

// get next create time in daily policy of rolling policy by default
func getCreateTime() int64 {
	timeStr := time.Now().Format("2006-01-02 00:00:00")
	t, _ := time.Parse("2006-01-02 00:00:00", timeStr)
	d, _ := time.ParseDuration("+24h")
	return t.Add(d).Unix()
}

func isInToday(createTime int64) bool {
	now := time.Now().Unix()

	if now > createTime {
		return true
	}

	return false
}
func (l *Logger) output(header string, msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	buffer := l.getBuffer()
	message := msg

	buffer.WriteString(header)
	buffer.WriteString(": ")
	buffer.WriteString(message)
	buffer.WriteString("\n")
	data := buffer.Bytes()

	// log mode
	if l.isLogStderrMode() {
		os.Stdout.Write(data)
	}
	if l.isLogFileMode() {
		// rolling policy
		// 1, file max size
		// 2, daily
		if l.writer == nil ||
			(l.maxFileNum > 1 && ((l.maxSize == 0 && isInToday(l.createTime)) ||
				(l.maxSize > 0 && l.nBytes > l.maxSize))) {
			l.writer = l.createLogFile()
			l.nBytes = 0
		}
		n, _ := l.writer.Write(data)
		l.writer.Flush()
		l.nBytes += n
	}

	l.putBuffer(buffer)
}

func (l *Logger) printLastLog() {
	if l.prevLog.repeatNum > 0 {
		msg := fmt.Sprintf("Last message repeated %d times", l.prevLog.repeatNum)
		l.output(l.prevLog.lastHeader, msg)
	}
}

func (l *Logger) putBuffer(b *logBuffer) {
	b.next = l.buffer
	l.buffer = b
}

func (l *Logger) close() {
	l.printLastLog()

	if l.writer == nil {
		return
	}
	l.writer.Sync()
	l.writer.Fclose()
}
