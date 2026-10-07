// Copyright 2026 Candace Labs

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/io/ipc/docker"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/net/model/ollama"
	ionet "github.com/candacelabs/csf/io/net"
	iohttp "github.com/candacelabs/csf/io/net/http"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/atomicfile"
	ouroborosv1 "github.com/candacelabs/csf/proto/candace/ouroboros/v1"
	"github.com/candacelabs/csf/services/ouroboros/labeler"
)

// The label verb: the ouroboros labeler over the mining tickets named, with
// the local model, writing every label and the run record under the state
// directory for the ops view.
const (
	verbLabel = "label"

	ticketsFileFlag      = "tickets-file"
	ticketRepositoryFlag = "repo"
	modelFlag            = "model"
	modelEndpointFlag    = "model-endpoint"
	modelContainerFlag   = "model-container"
	checkoutFlag         = "checkout"
	minerFlag            = "miner"
	batchesFlag          = "batches"
	thinkFlag            = "think"

	defaultModel = "qwen3:8b"
	// modelClientTimeout bounds one model request: a batch of a few dozen
	// candidates evaluated at the measured rate takes well under a minute,
	// and a cold load adds five seconds; ten minutes is the generous bound a
	// shared GPU may need.
	modelClientTimeout = 10 * time.Minute
	minerSeparator     = "="
	commentMark        = "#"
)

var (
	errNoTicket         = errors.New("-ticket or -tickets-file is required")
	errNoRepository     = errors.New("-repo owner/name is required")
	errNoModelContainer = errors.New("-model-container is required: the model server's container is never counted as another GPU consumer")
	errMinerShape       = errors.New("-miner must be name=executable")
)

// labelSettings is what the label verb's flags choose.
type labelSettings struct {
	tickets        []int64
	repository     string
	modelName      string
	endpoint       string
	modelContainer string
	checkout       string
	miners         listFlag
	batches        int
	think          bool
	state          string
}

// label runs the labeler over the tickets named, one after another, and
// prints every label as it is judged. A ticket that fails is reported and
// the run goes on; the verb fails at the end if any did.
func label(ctx context.Context, launcher *proc.HostLauncher, arguments []string, output io.Writer, diagnostics io.Writer) error {
	settings, err := parseLabelFlags(arguments)
	if err != nil {
		return err
	}
	containers, err := docker.NewContainerHost()
	if err != nil {
		return err
	}
	defer func() { _ = containers.Close() }()
	service, err := newLabelService(settings, launcher, containers, output, diagnostics)
	if err != nil {
		return err
	}
	var failures []error
	for _, number := range settings.tickets {
		// A bare request, as the loop's queue rows are: the labeler reads the
		// ticket itself.
		_, err := service.Label(ctx, &ouroborosv1.LabelRequest{Ticket: number, Repository: settings.repository})
		if err != nil {
			fmt.Fprintf(diagnostics, "%s %s: ticket %d: %v\n", commandName, verbLabel, number, err)
			failures = append(failures, err)
		}
		if ctx.Err() != nil {
			break
		}
	}
	return errors.Join(append(failures, service.Finish())...)
}

// parseLabelFlags reads the verb's flags and checks what they require.
func parseLabelFlags(arguments []string) (labelSettings, error) {
	var settings labelSettings
	var tickets listFlag
	flags := flag.NewFlagSet(commandName+" "+verbLabel, flag.ContinueOnError)
	flags.Var(&tickets, ticketFlag, "mining ticket number; repeat for several")
	ticketsFile := flags.String(ticketsFileFlag, "", "file whose lines start with a ticket number (a TSV's first column)")
	flags.StringVar(&settings.repository, ticketRepositoryFlag, "", "owner/name of the repository the tickets are in")
	flags.StringVar(&settings.modelName, modelFlag, defaultModel, "the local model")
	flags.StringVar(&settings.endpoint, modelEndpointFlag, ollama.DefaultEndpoint, "the model server's URL")
	flags.StringVar(&settings.modelContainer, modelContainerFlag, "", "the model server's container name")
	flags.StringVar(&settings.checkout, checkoutFlag, "", "git checkout the commit corpus is read from")
	flags.Var(&settings.miners, minerFlag, "name=executable of a miner that exists; repeat for several")
	flags.IntVar(&settings.batches, batchesFlag, labeler.DefaultBatches, "batches of candidates per ticket")
	flags.BoolVar(&settings.think, thinkFlag, false, "let the model reason before it answers")
	stateDirectory := flags.String(stateFlag, "", "state directory (default ~/"+defaultStateDirectory+")")
	if err := flags.Parse(arguments); err != nil {
		return settings, err
	}
	if flags.NArg() != 0 {
		return settings, errUsage
	}
	numbers, err := ticketNumbers(tickets, *ticketsFile)
	if err != nil {
		return settings, err
	}
	settings.tickets = numbers
	switch {
	case len(numbers) == 0:
		return settings, errNoTicket
	case settings.repository == "":
		return settings, errNoRepository
	case settings.modelContainer == "":
		return settings, errNoModelContainer
	}
	settings.state, err = stateRoot(*stateDirectory)
	return settings, err
}

// newLabelService composes the labeler: the brain over the host network,
// the state directory's files, the containers and the two record sinks.
func newLabelService(settings labelSettings, launcher *proc.HostLauncher, containers *docker.ContainerHost, output io.Writer, diagnostics io.Writer) (*labeler.Labeler, error) {
	files, err := iofs.NewHostFiles(settings.state)
	if err != nil {
		return nil, err
	}
	client, err := iohttp.NewHTTPClient(ionet.NewHostNetwork(), iohttp.WithClientTimeout(modelClientTimeout))
	if err != nil {
		return nil, err
	}
	brainOptions := []ollama.OllamaBrainOption{ollama.WithEndpoint(settings.endpoint), ollama.WithContextWindow(labeler.DefaultContextWindow)}
	if settings.think {
		brainOptions = append(brainOptions, ollama.WithThinking())
	}
	brain, err := ollama.NewOllamaBrain(client, settings.modelName, brainOptions...)
	if err != nil {
		return nil, err
	}
	encoder := protojson.MarshalOptions{UseProtoNames: true}
	recordPath := filepath.Join(settings.state, labeler.RecordFile)
	options := []labeler.LabelerOption{
		labeler.WithBrain(brain, ollama.ProviderName+"/"+settings.modelName),
		labeler.WithRepository(settings.repository),
		labeler.WithState(settings.state, files),
		labeler.WithHidden(hiddenRunsOf(settings.state, files)),
		labeler.WithLauncher(launcher),
		labeler.WithContainers(containers, settings.modelContainer),
		labeler.WithBatches(settings.batches),
		labeler.WithLogger(slog.New(slog.NewJSONHandler(diagnostics, nil))),
		labeler.WithLabelSink(func(proposed *ouroborosv1.ProposedLabel) error {
			line, err := encoder.Marshal(proposed)
			if err != nil {
				return err
			}
			fmt.Fprintln(output, string(line))
			return appendLine(recordPath, line)
		}),
		labeler.WithRunSink(func(run *ouroborosv1.LabelerRun) error {
			content, err := encoder.Marshal(run)
			if err != nil {
				return err
			}
			return atomicfile.WriteFile(filepath.Join(settings.state, labeler.RunFile), content, hostFileMode)
		}),
	}
	if settings.checkout != "" {
		options = append(options, labeler.WithCheckout(settings.checkout))
	}
	for _, miner := range settings.miners {
		name, executable, found := strings.Cut(miner, minerSeparator)
		if !found {
			return nil, fmt.Errorf("%w: %q", errMinerShape, miner)
		}
		options = append(options, labeler.WithMiner(name, executable))
	}
	return labeler.NewLabeler(options...)
}

// ticketNumbers is every -ticket plus the first field of every line of the
// tickets file that starts with a number; other lines (a header, a comment)
// are skipped.
func ticketNumbers(flags listFlag, path string) ([]int64, error) {
	var numbers []int64
	for _, value := range flags {
		number, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("-%s %q: %w", ticketFlag, value, err)
		}
		numbers = append(numbers, number)
	}
	if path == "" {
		return numbers, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || strings.HasPrefix(fields[0], commentMark) {
			continue
		}
		number, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			continue
		}
		numbers = append(numbers, number)
	}
	return numbers, scanner.Err()
}

// appendLine appends one record line to a file under the state directory.
func appendLine(path string, line []byte) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, hostFileMode)
	if err != nil {
		return err
	}
	_, err = file.Write(append(line, '\n'))
	return errors.Join(err, file.Close())
}
