package api

import (
	"context"

	"gitlab.com/slon/shad-go/distbuild/pkg/build"
)

// JobResult описывает результат работы джоба.
type JobResult struct {
	ID build.ID

	Stdout, Stderr []byte

	ExitCode int

	// Error описывает сообщение об ошибке, из-за которого джоб не удалось выполнить.
	//
	// Если Error == nil, значит джоб завершился успешно.
	Error *string

	// id билда для которого выполнена эта джоба (или взят из кэша результат)
	buildID build.ID
}

type WorkerID string

func (w WorkerID) String() string {
	return string(w)
}

type HeartbeatRequest struct {
	// WorkerID задаёт персистентный идентификатор данного воркера.
	//
	// WorkerID также выступает в качестве endpoint-а, к которому можно подключиться по HTTP.
	//
	// В наших тестах идентификатор будет иметь вид "localhost:%d".
	WorkerID WorkerID

	// RunningJobs перечисляет список джобов, которые выполняются на этом воркере
	// в данный момент.
	RunningJobs []build.ID

	// FreeSlots сообщает, сколько еще процессов можно запустить на этом воркере.
	FreeSlots int

	// JobResult сообщает координатору, какие джобы завершили исполнение на этом воркере
	// на этой итерации цикла.
	FinishedJob []JobResult

	// AddedArtifacts говорит, какие артефакты появились в кеше на этой итерации цикла.
	AddedArtifacts []build.ID
}

// JobSpec описывает джоб, который нужно запустить.
type JobSpec struct {
	// SourceFiles задаёт список файлов, который должны присутствовать в директории с исходным кодом при запуске этого джоба.
	SourceFiles map[build.ID]string

	// Artifacts задаёт воркеров, с которых можно скачать артефакты необходимые этому джобу.
	Artifacts map[build.ID]WorkerID

	build.Job

	// id билда для которого мы выполняем эту джобу
	buildID build.ID
}

type HeartbeatResponse struct {
	JobsToRun map[build.ID]JobSpec
}

type HeartbeatService interface {
	Heartbeat(ctx context.Context, req *HeartbeatRequest) (*HeartbeatResponse, error)
}
