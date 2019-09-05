// Copyright 2015 ThoughtWorks, Inc.

// This file is part of Gauge.

// Gauge is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Gauge is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.

// You should have received a copy of the GNU General Public License
// along with Gauge.  If not, see <http://www.gnu.org/licenses/>.

package execution

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"

	"github.com/getgauge/gauge/config"
	"github.com/getgauge/gauge/logger"

	// "encoding/base64"
	"github.com/getgauge/gauge/execution/event"
	"github.com/getgauge/gauge/execution/result"
	"github.com/getgauge/gauge/gauge"
	"github.com/getgauge/gauge/gauge_messages"
	"github.com/getgauge/gauge/plugin"
	"github.com/getgauge/gauge/runner"
	uuid "github.com/satori/go.uuid"
)

type stepExecutor struct {
	runner               runner.Runner
	pluginHandler        plugin.Handler
	currentExecutionInfo *gauge_messages.ExecutionInfo
	stream               int
}

// TODO: stepExecutor should not consume both gauge.Step and gauge_messages.ProtoStep. The usage of ProtoStep should be eliminated.
func (e *stepExecutor) executeStep(step *gauge.Step, protoStep *gauge_messages.ProtoStep) *result.StepResult {
	stepRequest := e.createStepRequest(protoStep)
	e.currentExecutionInfo.CurrentStep = &gauge_messages.StepInfo{Step: stepRequest, IsFailed: false}
	stepResult := result.NewStepResult(protoStep)
	for i := range step.GetFragments() {
		stepFragmet := step.GetFragments()[i]
		protoStepFragmet := protoStep.GetFragments()[i]
		if stepFragmet.FragmentType == gauge_messages.Fragment_Parameter && stepFragmet.Parameter.ParameterType == gauge_messages.Parameter_Dynamic {
			stepFragmet.GetParameter().Value = protoStepFragmet.GetParameter().Value
		}
	}
	event.Notify(event.NewExecutionEvent(event.StepStart, step, nil, e.stream, *e.currentExecutionInfo))

	e.notifyBeforeStepHook(stepResult)
	if !stepResult.GetFailed() {
		executeStepMessage := &gauge_messages.Message{MessageType: gauge_messages.Message_ExecuteStep, ExecuteStepRequest: stepRequest}
		stepExecutionStatus := e.runner.ExecuteAndGetStatus(executeStepMessage)
		stepExecutionStatus.Message = append(stepResult.ProtoStepExecResult().GetExecutionResult().Message, stepExecutionStatus.Message...)
		// stepExecutionStatus.Screenshots = append(stepResult.ProtoStepExecResult().GetExecutionResult().Screenshots, stepExecutionStatus.Screenshots...)
		logger.Infof(true, "stepExecutionStatus %d Screenshots, \n\n", len(stepExecutionStatus.Screenshots))
		for _, screenshot := range stepExecutionStatus.Screenshots {
			logger.Info(true, "Creating screenshot \n\n")
			bytesReader := bytes.NewReader(screenshot)
			logger.Info(true, "Creating bytes.NewReader \n\n")
			image, err := png.Decode(bytesReader)
			logger.Info(true, "Creating png.Decode \n\n")
			if err != nil {
				logger.Errorf(true, "Unable to create image Error : %s\n", err.Error())
			}
			os.MkdirAll(filepath.Join(config.ProjectRoot, "reports", "html-report", "screenshots"), 0755)
			file, err := os.OpenFile(filepath.Join(config.ProjectRoot, "reports", "html-report", "screenshots", fmt.Sprintf("%s.png", uuid.NewV4().String())), os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				logger.Errorf(true, "Unable to create image Error : %s\n", err.Error())
			}
			logger.Infof(true, "Creating png.Encode %s \n\n", file.Name())
			err = png.Encode(file, image)
			if err == nil {
				logger.Infof(true, "Saving screenshot at : %s", file.Name())
				stepExecutionStatus.ScreenshotFiles = append(stepExecutionStatus.ScreenshotFiles, file.Name())
				file.Close()
			}
		}
		if stepExecutionStatus.GetFailed() {
			e.currentExecutionInfo.CurrentStep.ErrorMessage = stepExecutionStatus.GetErrorMessage()
			e.currentExecutionInfo.CurrentStep.StackTrace = stepExecutionStatus.GetStackTrace()
			setStepFailure(e.currentExecutionInfo)
			stepResult.SetStepFailure()
		}
		if stepExecutionStatus.FailureScreenshot != nil {
			bytesReader := bytes.NewReader(stepExecutionStatus.FailureScreenshot)
			image, err := png.Decode(bytesReader)
			logger.Info(true, "Creating png.Decode \n\n")
			if err != nil {
				logger.Errorf(true, "Unable to create image Error : %s\n", err.Error())
			}
			os.MkdirAll(filepath.Join(config.ProjectRoot, "reports", "html-report", "failure_screenshots"), 0755)
			file, err := os.OpenFile(filepath.Join(config.ProjectRoot, "reports", "html-report", "failure_screenshots", fmt.Sprintf("%s.png", uuid.NewV4().String())), os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				logger.Errorf(true, "Unable to create image Error : %s\n", err.Error())
			}
			logger.Infof(true, "Creating png.Encode %s \n\n", file.Name())
			err = png.Encode(file, image)
			if err == nil {
				logger.Infof(true, "Saving screenshot at : %s", file.Name())
				file.Close()
			}
			stepExecutionStatus.FailureScreenshot = []byte{}
			stepExecutionStatus.ScreenShot = []byte{}
			stepExecutionStatus.FailureScreenshotFile = file.Name()
		}
		stepResult.SetProtoExecResult(stepExecutionStatus)
	}
	e.notifyAfterStepHook(stepResult)

	event.Notify(event.NewExecutionEvent(event.StepEnd, *step, stepResult, e.stream, *e.currentExecutionInfo))
	defer e.currentExecutionInfo.CurrentStep.Reset()
	return stepResult
}

func (e *stepExecutor) createStepRequest(protoStep *gauge_messages.ProtoStep) *gauge_messages.ExecuteStepRequest {
	stepRequest := &gauge_messages.ExecuteStepRequest{ParsedStepText: protoStep.GetParsedText(), ActualStepText: protoStep.GetActualText()}
	stepRequest.Parameters = getParameters(protoStep.GetFragments())
	return stepRequest
}

func (e *stepExecutor) notifyBeforeStepHook(stepResult *result.StepResult) {
	m := &gauge_messages.Message{
		MessageType:                  gauge_messages.Message_StepExecutionStarting,
		StepExecutionStartingRequest: &gauge_messages.StepExecutionStartingRequest{CurrentExecutionInfo: e.currentExecutionInfo},
	}
	e.pluginHandler.NotifyPlugins(m)
	res := executeHook(m, stepResult, e.runner)
	stepResult.ProtoStep.PreHookMessages = res.Message
	stepResult.ProtoStep.PreHookScreenshots = res.Screenshots
	if res.GetFailed() {
		setStepFailure(e.currentExecutionInfo)
		handleHookFailure(stepResult, res, result.AddPreHook)
	}
	m.StepExecutionStartingRequest.StepResult = gauge.ConvertToProtoStepResult(stepResult)
	e.pluginHandler.NotifyPlugins(m)
}

func (e *stepExecutor) notifyAfterStepHook(stepResult *result.StepResult) {
	m := &gauge_messages.Message{
		MessageType:                gauge_messages.Message_StepExecutionEnding,
		StepExecutionEndingRequest: &gauge_messages.StepExecutionEndingRequest{CurrentExecutionInfo: e.currentExecutionInfo},
	}

	res := executeHook(m, stepResult, e.runner)
	stepResult.ProtoStep.PostHookMessages = res.Message
	stepResult.ProtoStep.PostHookScreenshots = res.Screenshots
	if res.GetFailed() {
		setStepFailure(e.currentExecutionInfo)
		handleHookFailure(stepResult, res, result.AddPostHook)
	}
	m.StepExecutionEndingRequest.StepResult = gauge.ConvertToProtoStepResult(stepResult)
	e.pluginHandler.NotifyPlugins(m)
}
