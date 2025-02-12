# techrocks

A CLI tool for generating beautiful blog posts based on markdown documents

## usage

```
./techrocks -input post.md -output output.html -template template.html
```

It may also be used with these default values:

-   input=post.md
-   template=template.html
-   output=output.html

Usage like:

```
./techrocks
```

## building

```
go build -o techrocks ./main.go
```
