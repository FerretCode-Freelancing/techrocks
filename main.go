package main

import (
	"bytes"
	"flag"
	"html/template"
	"log/slog"
	"os"

	"github.com/yuin/goldmark"
)

type PageData struct {
	Content template.HTML
}

func main() {
	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{})
	logger := slog.New(handler)

	markdownDocument := flag.String("markdown", "post.md", "the input markdown post file")
	templateFile := flag.String("template", "template.html", "the template file for the markdown document")
	outputFile := flag.String("output", "output.html", "the output html file")

	flag.Parse()

	if markdownDocument == nil || *markdownDocument == "" {
		logger.Error("the markdown document flag must be present")
		return
	}

	if templateFile == nil || *templateFile == "" {
		logger.Error("the template file must be present")
		return
	}

	if outputFile == nil || *outputFile == "" {
		logger.Error("the output file must be present")
		return
	}

	markdownContent, err := os.ReadFile(*markdownDocument)
	if err != nil {
		logger.Error("there was an error parsing the markdown content", "err", err)
		return
	}

	var buf bytes.Buffer
	if err := goldmark.Convert(markdownContent, &buf); err != nil {
		logger.Error("error converting markdown into html", "err", err)
		return
	}

	tmpl, err := template.ParseFiles(*templateFile)
	if err != nil {
		logger.Error("error parsing template file", "err", err)
		return
	}

	output, err := os.Create(*outputFile)
	if err != nil {
		logger.Error("error creating output file", "err", err)
		return
	}
	defer output.Close()

	data := PageData{
		Content: template.HTML(buf.String()),
	}
	if err := tmpl.Execute(output, data); err != nil {
		logger.Error("error rendering template with markdown", "err", err)
		return
	}

	logger.Info("the template was successfully rendered", "input", *markdownDocument, "template", *templateFile, "output", *outputFile)
}
