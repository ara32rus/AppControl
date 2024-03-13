package main

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	whiteListFile = "whitelist.txt"
	allowedProcs  = loadWhiteList()
	procMutex     sync.Mutex
	sendSigKill   bool
)

func main() {
	// Устанавливаем обработчик сигнала завершения
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-signalChannel
		fmt.Println("\nПрограмма завершена.")
		os.Exit(0)
	}()

	// Отправка SIGKILL для уже запущенных процессов через 10 секунд
	go func() {
		time.Sleep(time.Second * 10)
		sendSigKill = true
	}()

	// Мониторим процессы
	for {
		runningProcs, err := getRunningProcesses()
		if err != nil {
			fmt.Println("Ошибка при получении списка запущенных процессов:", err)
			continue
		}

		// Блокируем запуск процессов, не входящих в белый список
		blockUnauthorizedProcesses(runningProcs)

		// Пауза перед следующей итерацией мониторинга
		// Здесь вы можете настроить интервал времени по вашему усмотрению
		//time.Sleep(time.Second * 10)
	}
}

func loadWhiteList() map[string]struct{} {
	whiteList := make(map[string]struct{})
	file, err := os.Open(whiteListFile)
	if err != nil {
		fmt.Println("Ошибка при открытии файла белого списка:", err)
		os.Exit(1)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		processName := strings.TrimSpace(scanner.Text())
		if processName != "" {
			whiteList[processName] = struct{}{}
		}
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Ошибка при чтении файла белого списка:", err)
		os.Exit(1)
	}

	return whiteList
}

func getRunningProcesses() ([]string, error) {
	procs := make(map[string]struct{}) // Используем мап для хранения уникальных PID процессов

	// Открываем дескриптор /proc для чтения информации о процессах
	dir, err := os.Open("/proc")
	if err != nil {
		return nil, fmt.Errorf("не удалось открыть /proc: %v", err)
	}
	defer dir.Close()

	// Получаем список всех поддиректорий в /proc
	files, err := dir.Readdirnames(0)
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения содержимого /proc: %v", err)
	}

	// Фильтруем только числовые директории (процессы)
	for _, file := range files {
		if pid, isNumeric := isNumericDir(file); isNumeric && isNonKernelProcess(pid) {
			procs[pid] = struct{}{}
		}
	}

	// Преобразуем мап обратно в срез для возврата
	result := make([]string, 0, len(procs))
	for pid := range procs {
		result = append(result, pid)
	}

	return result, nil
}

func isNumericDir(dirName string) (string, bool) {
	if _, err := os.Stat("/proc/" + dirName); err != nil {
		return "", false
	}

	// Проверяем, является ли имя директории числовым
	if _, err := strconv.Atoi(dirName); err == nil {
		return dirName, true
	}

	return "", false
}

func isNonKernelProcess(pid string) bool {
	pidInt, err := strconv.Atoi(pid)
	if err != nil {
		fmt.Printf("Ошибка при преобразовании PID %s: %v\n", pid, err)
		return false
	}

	// Определяем пороговое значение, ниже которого считаем, что это процесс ядра
	kernelThreshold := 1000

	return pidInt >= kernelThreshold
}

func blockUnauthorizedProcesses(runningProcs []string) {
	procMutex.Lock()
	defer procMutex.Unlock()

	for _, proc := range runningProcs {
		procName, err := getProcessName(proc)
		if err != nil {
			fmt.Printf("Ошибка при получении имени процесса %s: %v\n", proc, err)
			continue
		}

		if _, allowed := allowedProcs[procName]; !allowed {
			fmt.Printf("Блокирован запуск неразрешенного процесса: %s (PID %s)\n", procName, proc)
			if sendSigKill {
				fmt.Printf("Отправляем SIGKILL для процесса %s (PID %s)\n", procName, proc)
				//terminateProcess(proc)
			}

			filename := "whitelist.txt"

			err := writeStringToFile(filename, procName)
			if err != nil {
				fmt.Printf("Ошибка: %v\n", err)
			}
		}
	}
}

func getProcessName(pid string) (string, error) {
	// Читаем содержимое /proc/<pid>/comm, чтобы получить имя процесса
	commPath := fmt.Sprintf("/proc/%s/comm", pid)
	content, err := os.ReadFile(commPath)
	if err != nil {
		return "", fmt.Errorf("не удалось прочитать %s: %v", commPath, err)
	}

	return strings.TrimSpace(string(content)), nil
}

func terminateProcess(pid string) {
	pidInt, err := strconv.Atoi(pid)
	if err != nil {
		fmt.Printf("Ошибка при преобразовании PID %s: %v\n", pid, err)
		return
	}

	// Отправляем сигнал завершения процессу
	err = syscall.Kill(pidInt, syscall.SIGKILL)
	if err != nil {
		fmt.Printf("Ошибка при отправке сигнала SIGKILL процессу %s: %v\n", pid, err)
		// Здесь можно предпринять дополнительные действия, если не удалось отправить SIGKILL
	}
}

func writeStringToFile(filename string, data string) error {
	// Читаем текущее содержимое файла
	currentData, err := ioutil.ReadFile(filename)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("ошибка при чтении файла: %v", err)
	}

	// Проверяем, содержится ли уже такая строка
	if strings.Contains(string(currentData), data) {
		return nil
	}

	// Открываем файл для записи
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return fmt.Errorf("не удалось открыть файл для записи: %v", err)
	}
	defer file.Close()

	// Записываем новую строку в файл
	_, err = file.WriteString(data + "\n")
	if err != nil {
		return fmt.Errorf("ошибка при записи в файл: %v", err)
	}

	fmt.Printf("Строка '%s' успешно добавлена в файл %s\n", data, filename)
	return nil
}
