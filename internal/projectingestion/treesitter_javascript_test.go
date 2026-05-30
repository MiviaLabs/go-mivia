package projectingestion

import (
	"context"
	"testing"
)

func TestTreeSitterJavaScriptExtractsSymbols(t *testing.T) {
	result := parseWithExtractor(t, newTreeSitterJavaScriptExtractor(), "src/app.js", []byte(`
import express from "express";
export function handler() {
	return true;
}
export class Controller {}
const load = async () => true;
`))

	assertSymbol(t, result.Symbols, SymbolKindImport, "express", "express", "", 2, 2)
	assertSymbol(t, result.Symbols, SymbolKindFunction, "handler", "", "", 3, 5)
	assertSymbol(t, result.Symbols, SymbolKindClass, "Controller", "", "", 6, 6)
	assertSymbol(t, result.Symbols, SymbolKindFunction, "load", "", "", 7, 7)
}

func TestTreeSitterTypeScriptExtractsSymbols(t *testing.T) {
	result := parseWithExtractor(t, newTreeSitterTypeScriptExtractor(), "src/app.ts", []byte(`
import { Client } from "@scope/pkg";
export interface Options {
	enabled: boolean;
}
export type Mode = "fast";
export enum State { Ready }
export class Service {}
export const build = () => new Service();
`))

	assertSymbol(t, result.Symbols, SymbolKindImport, "@scope/pkg", "@scope/pkg", "", 2, 2)
	assertSymbol(t, result.Symbols, SymbolKindType, "Options", "", "", 3, 5)
	assertSymbol(t, result.Symbols, SymbolKindType, "Mode", "", "", 6, 6)
	assertSymbol(t, result.Symbols, SymbolKindType, "State", "", "", 7, 7)
	assertSymbol(t, result.Symbols, SymbolKindClass, "Service", "", "", 8, 8)
	assertSymbol(t, result.Symbols, SymbolKindFunction, "build", "", "", 9, 9)
}

func TestTreeSitterTSXExtractsSymbols(t *testing.T) {
	result := parseWithExtractor(t, newTreeSitterTSXExtractor(), "src/App.tsx", []byte(`
import React from "react";
export function App() {
	return <main />;
}
export class Shell extends React.Component {}
export const Widget = () => <section />;
`))

	assertSymbol(t, result.Symbols, SymbolKindImport, "react", "react", "", 2, 2)
	assertSymbol(t, result.Symbols, SymbolKindFunction, "App", "", "", 3, 5)
	assertSymbol(t, result.Symbols, SymbolKindClass, "Shell", "", "", 6, 6)
	assertSymbol(t, result.Symbols, SymbolKindFunction, "Widget", "", "", 7, 7)
}

func TestTreeSitterPythonExtractsSymbols(t *testing.T) {
	result := parseWithExtractor(t, newTreeSitterPythonExtractor(), "src/app.py", []byte(`
import os
from package.module import Client

class Service:
    def run(self):
        return True

def build_service():
    return Service()
`))

	assertSymbol(t, result.Symbols, SymbolKindImport, "os", "os", "", 2, 2)
	assertSymbol(t, result.Symbols, SymbolKindImport, "package.module", "package.module", "", 3, 3)
	assertSymbol(t, result.Symbols, SymbolKindClass, "Service", "", "", 5, 7)
	assertSymbol(t, result.Symbols, SymbolKindFunction, "run", "", "", 6, 7)
	assertSymbol(t, result.Symbols, SymbolKindFunction, "build_service", "", "", 9, 10)
}

func TestBadTypeScriptSyntaxRecordsParseError(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root+"/good.ts", "export function ok() { return true }\n")
	writeFile(t, root+"/bad.ts", "export function broken( {\n")

	svc, _, state := newTestService(t, root)
	run, err := svc.IngestProject(ctx, "example-service", TriggerManual)
	if err != nil {
		t.Fatalf("ingest project: %v", err)
	}
	if run.Status != RunStatusCompleted || run.ErrorCategory != "file_errors" {
		t.Fatalf("expected completed run with file errors, got %#v", run)
	}
	skipped, err := state.ListFileStates(ctx, "example-service", FileStateFilter{Status: FileStatusSkipped})
	if err != nil {
		t.Fatalf("list skipped states: %v", err)
	}
	if len(skipped) != 1 || skipped[0].SkippedReason != SkipReasonParseError || skipped[0].ContentSHA256 != "" {
		t.Fatalf("expected parse-error skip without content hash, got %#v", skipped)
	}
}

func TestTreeSitterExtractorLifecycleValidation(t *testing.T) {
	for _, extractor := range []Extractor{
		newTreeSitterJavaScriptExtractor(),
		newTreeSitterTypeScriptExtractor(),
		newTreeSitterTSXExtractor(),
		newTreeSitterPythonExtractor(),
	} {
		if err := extractor.Validate(); err != nil {
			t.Fatalf("validate %s: %v", extractor.Name(), err)
		}
	}
}

func parseWithExtractor(t *testing.T, extractor Extractor, relative string, source []byte) ExtractorResult {
	t.Helper()
	result, err := extractor.Parse(context.Background(), relative, source)
	if err != nil {
		t.Fatalf("parse %s: %v", relative, err)
	}
	return result
}
