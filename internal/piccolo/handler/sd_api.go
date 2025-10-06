package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/go-logr/logr"
	"github.com/laixintao/piccolo/internal/piccolo/model"
	"github.com/laixintao/piccolo/internal/piccolo/storage"
)

type ImageHandler struct {
	store storage.ImageStore
	log   logr.Logger
}

func NewImageHandler(store storage.ImageStore, log logr.Logger) *ImageHandler {
	return &ImageHandler{
		store: store,
		log:   log,
	}
}

// CreateImage 创建镜像记录的API处理器
// POST /api/v1/images
func (h *ImageHandler) CreateImage(c *gin.Context) {
	var req model.CreateImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error(err, "failed to bind JSON request")
		c.JSON(http.StatusBadRequest, model.CreateImageResponse{
			Success: false,
			Message: "无效的请求格式: " + err.Error(),
		})
		return
	}

	// 检查是否已存在相同digest的镜像
	existingImage, err := h.store.GetImageByDigest(string(req.Digest))
	if err == nil && existingImage != nil {
		h.log.Info("image already exists", "digest", req.Digest)
		c.JSON(http.StatusConflict, model.CreateImageResponse{
			Success: false,
			Message: "镜像已存在",
			Image:   existingImage,
		})
		return
	}

	// 创建新的镜像记录
	imageRecord := &model.ImageRecord{
		Name:       req.Name,
		Registry:   req.Registry,
		Repository: req.Repository,
		Tag:        req.Tag,
		Digest:     string(req.Digest),
		Size:       req.Size,
		Metadata:   req.Metadata,
	}

	if err := h.store.CreateImage(imageRecord); err != nil {
		h.log.Error(err, "failed to create image record")
		c.JSON(http.StatusInternalServerError, model.CreateImageResponse{
			Success: false,
			Message: "创建镜像记录失败: " + err.Error(),
		})
		return
	}

	h.log.Info("image created successfully", "uuid", imageRecord.UUID, "name", imageRecord.Name)
	c.JSON(http.StatusCreated, model.CreateImageResponse{
		Success: true,
		Message: "镜像记录创建成功",
		Image:   imageRecord,
	})
}

// GetImage 获取单个镜像记录的API处理器
// GET /api/v1/images/:uuid
func (h *ImageHandler) GetImage(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "镜像UUID不能为空",
		})
		return
	}

	image, err := h.store.GetImage(uuid)
	if err != nil {
		h.log.Error(err, "failed to get image", "uuid", uuid)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "镜像不存在",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "获取镜像成功",
		"image":   image,
	})
}

// ListImages 获取镜像列表的API处理器
// GET /api/v1/images
func (h *ImageHandler) ListImages(c *gin.Context) {
	// 获取查询参数
	limitStr := c.DefaultQuery("limit", "100")
	offsetStr := c.DefaultQuery("offset", "0")

	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 100
	}

	offset, err := strconv.Atoi(offsetStr)
	if err != nil || offset < 0 {
		offset = 0
	}

	images, err := h.store.ListImages()
	if err != nil {
		h.log.Error(err, "failed to list images")
		c.JSON(http.StatusInternalServerError, model.ListImagesResponse{
			Success: false,
			Message: "获取镜像列表失败: " + err.Error(),
		})
		return
	}

	total := len(images)
	
	// 简单的分页处理
	start := offset
	end := offset + limit
	if start > total {
		start = total
	}
	if end > total {
		end = total
	}

	var pagedImages []*model.ImageRecord
	if start < end {
		pagedImages = images[start:end]
	} else {
		pagedImages = []*model.ImageRecord{}
	}

	h.log.Info("listed images", "total", total, "returned", len(pagedImages))
	c.JSON(http.StatusOK, model.ListImagesResponse{
		Success: true,
		Message: "获取镜像列表成功",
		Images:  pagedImages,
		Total:   int64(total),
	})
}

// UpdateImage 更新镜像记录的API处理器
// PUT /api/v1/images/:uuid
func (h *ImageHandler) UpdateImage(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "镜像UUID不能为空",
		})
		return
	}

	var req model.CreateImageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Error(err, "failed to bind JSON request")
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的请求格式: " + err.Error(),
		})
		return
	}

	// 获取现有记录
	existingImage, err := h.store.GetImage(uuid)
	if err != nil {
		h.log.Error(err, "failed to get existing image", "uuid", uuid)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "镜像不存在",
		})
		return
	}

	// 更新字段
	existingImage.Name = req.Name
	existingImage.Registry = req.Registry
	existingImage.Repository = req.Repository
	existingImage.Tag = req.Tag
	existingImage.Digest = string(req.Digest)
	existingImage.Size = req.Size
	existingImage.Metadata = req.Metadata

	if err := h.store.UpdateImage(existingImage); err != nil {
		h.log.Error(err, "failed to update image record")
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "更新镜像记录失败: " + err.Error(),
		})
		return
	}

	h.log.Info("image updated successfully", "uuid", uuid)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "镜像记录更新成功",
		"image":   existingImage,
	})
}

// DeleteImage 删除镜像记录的API处理器
// DELETE /api/v1/images/:uuid
func (h *ImageHandler) DeleteImage(c *gin.Context) {
	uuid := c.Param("uuid")
	if uuid == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "镜像UUID不能为空",
		})
		return
	}

	// 检查镜像是否存在
	_, err := h.store.GetImage(uuid)
	if err != nil {
		h.log.Error(err, "image not found", "uuid", uuid)
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "镜像不存在",
		})
		return
	}

	if err := h.store.DeleteImage(uuid); err != nil {
		h.log.Error(err, "failed to delete image record")
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"message": "删除镜像记录失败: " + err.Error(),
		})
		return
	}

	h.log.Info("image deleted successfully", "uuid", uuid)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "镜像记录删除成功",
	})
}
