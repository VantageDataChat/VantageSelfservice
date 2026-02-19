package cli

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"askflow/internal/backup"
	"askflow/internal/document"
	"askflow/internal/handler"
	"askflow/internal/product"
)

// RunBatchImport scans directories and imports supported files.
func RunBatchImport(args []string, dm *document.DocumentManager, ps *product.ProductService) {
	// Parse --product flag
	var productID string
	var dirs []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--product" {
			if i+1 >= len(args) {
				fmt.Println("错误: --product 参数需要指定产品 ID")
				fmt.Println("用法: askflow import [--product <product_id>] <目录> [...]")
				os.Exit(1)
			}
			productID = args[i+1]
			i++ // skip the value
		} else {
			dirs = append(dirs, args[i])
		}
	}

	if len(dirs) == 0 {
		fmt.Println("错误: 请指定至少一个目录路径")
		fmt.Println("用法: askflow import [--product <product_id>] <目录> [...]")
		os.Exit(1)
	}

	// Validate product ID if provided
	if productID != "" {
		p, err := ps.GetByID(productID)
		if err != nil || p == nil {
			fmt.Printf("错误: 指定的产品不存在 (ID: %s)\n", productID)
			os.Exit(1)
		}
		fmt.Printf("目标产品: %s (%s)\n", p.Name, p.ID)
	} else {
		fmt.Println("目标: 公共库")
	}

	// Collect all files to import
	var files []string
	for _, dir := range dirs {
		info, err := os.Stat(dir)
		if err != nil {
			fmt.Printf("警告: 无法访问 %s: %v\n", dir, err)
			continue
		}
		if !info.IsDir() {
			// Single file
			if _, ok := handler.SupportedExtensions[strings.ToLower(filepath.Ext(dir))]; ok {
				files = append(files, dir)
			} else {
				fmt.Printf("跳过: 不支持的文件格式 %s\n", dir)
			}
			continue
		}
		// Walk directory
		filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				fmt.Printf("警告: 无法访问 %s: %v\n", path, err)
				return nil
			}
			if fi.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(fi.Name()))
			if _, ok := handler.SupportedExtensions[ext]; ok {
				files = append(files, path)
			}
			return nil
		})
	}

	if len(files) == 0 {
		fmt.Println("未找到支持的文件")
		return
	}

	fmt.Printf("找到 %d 个文件，开始导入...\n\n", len(files))

	type failedFile struct {
		Path   string
		Reason string
	}
	var success, failed int
	var failedFiles []failedFile
	for i, filePath := range files {
		fileName := filepath.Base(filePath)
		ext := strings.ToLower(filepath.Ext(fileName))
		fileType := handler.SupportedExtensions[ext]

		fmt.Printf("[%d/%d] %s ... ", i+1, len(files), filePath)

		fileData, err := os.ReadFile(filePath)
		if err != nil {
			reason := fmt.Sprintf("读取失败: %v", err)
			fmt.Println(reason)
			failed++
			failedFiles = append(failedFiles, failedFile{Path: filePath, Reason: reason})
			continue
		}

		req := document.UploadFileRequest{
			FileName:  fileName,
			FileData:  fileData,
			FileType:  fileType,
			ProductID: productID,
		}
		doc, err := dm.UploadFile(req)
		if err != nil {
			reason := fmt.Sprintf("导入失败: %v", err)
			fmt.Println(reason)
			failed++
			failedFiles = append(failedFiles, failedFile{Path: filePath, Reason: reason})
			continue
		}
		if doc.Status == "failed" {
			reason := fmt.Sprintf("处理失败: %s", doc.Error)
			fmt.Println(reason)
			failed++
			failedFiles = append(failedFiles, failedFile{Path: filePath, Reason: reason})
			continue
		}

		fmt.Printf("成功 (ID: %s)\n", doc.ID)
		success++
	}

	fmt.Println("\n========== 导入报告 ==========")
	fmt.Printf("总文件数: %d\n", len(files))
	fmt.Printf("成功文件数: %d\n", success)
	fmt.Printf("失败文件数: %d\n", failed)
	if len(failedFiles) > 0 {
		fmt.Println("\n失败文件列表:")
		for _, f := range failedFiles {
			absPath, err := filepath.Abs(f.Path)
			if err != nil {
				absPath = f.Path
			}
			fmt.Printf("  %s\n    原因: %s\n", absPath, f.Reason)
		}
	}
	fmt.Println("==============================")
}

// RunBackup executes a full or incremental backup of the data directory.
func RunBackup(args []string, db *sql.DB) {
	opts := backup.Options{
		DataDir: "./data",
		Mode:    "full",
	}

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output", "-o":
			if i+1 >= len(args) {
				fmt.Println("错误: --output 需要指定目录")
				os.Exit(1)
			}
			opts.OutputDir = args[i+1]
			i++
		case "--incremental":
			opts.Mode = "incremental"
		case "--base":
			if i+1 >= len(args) {
				fmt.Println("错误: --base 需要指定 manifest 文件路径")
				os.Exit(1)
			}
			opts.ManifestIn = args[i+1]
			i++
		default:
			fmt.Printf("未知参数: %s\n", args[i])
			fmt.Println("用法: askflow backup [--output <目录>] [--incremental --base <manifest>]")
			os.Exit(1)
		}
	}

	if opts.OutputDir != "" {
		if err := os.MkdirAll(opts.OutputDir, 0755); err != nil {
			fmt.Printf("创建输出目录失败: %v\n", err)
			os.Exit(1)
		}
	}

	fmt.Printf("开始%s备份...\n", map[string]string{"full": "全量", "incremental": "增量"}[opts.Mode])

	result, err := backup.Run(db, opts)
	if err != nil {
		fmt.Printf("备份失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("备份完成:\n")
	fmt.Printf("  归档文件: %s\n", result.ArchivePath)
	fmt.Printf("  Manifest: %s\n", result.ManifestPath)
	fmt.Printf("  文件数: %d, 数据库行数: %d\n", result.FilesWritten, result.DBRows)
	fmt.Printf("  归档大小: %.2f MB\n", float64(result.BytesWritten)/(1024*1024))
}

// RunRestore restores data from a backup archive.
func RunRestore(args []string) {
	targetDir := "./data"
	var archivePath string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--target", "-t":
			if i+1 >= len(args) {
				fmt.Println("错误: --target 需要指定目录")
				os.Exit(1)
			}
			targetDir = args[i+1]
			i++
		default:
			if archivePath != "" {
				fmt.Printf("未知参数: %s\n", args[i])
				os.Exit(1)
			}
			archivePath = args[i]
		}
	}

	if archivePath == "" {
		fmt.Println("错误: 请指定备份文件路径")
		fmt.Println("用法: askflow restore [--target <目录>] <备份文件>")
		os.Exit(1)
	}

	fmt.Printf("从 %s 恢复数据到 %s ...\n", archivePath, targetDir)
	if err := backup.Restore(archivePath, targetDir); err != nil {
		fmt.Printf("恢复失败: %v\n", err)
		os.Exit(1)
	}
}

// RunListProducts lists all products with their IDs.
func RunListProducts(ps *product.ProductService) {
	products, err := ps.List()
	if err != nil {
		fmt.Printf("查询产品列表失败: %v\n", err)
		os.Exit(1)
	}
	if len(products) == 0 {
		fmt.Println("暂无产品")
		return
	}
	fmt.Printf("%-34s  %-20s  %s\n", "产品 ID", "名称", "描述")
	fmt.Println(strings.Repeat("-", 80))
	for _, p := range products {
		desc := p.Description
		if len(desc) > 30 {
			desc = desc[:30] + "..."
		}
		fmt.Printf("%-34s  %-20s  %s\n", p.ID, p.Name, desc)
	}
	fmt.Printf("\n共 %d 个产品\n", len(products))
}
