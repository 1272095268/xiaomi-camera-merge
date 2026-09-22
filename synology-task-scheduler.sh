#!/bin/sh
# ============================================================================
# 小米摄像头录像合并 - 群晖任务计划脚本（定时自动运行）
# ----------------------------------------------------------------------------
# 放置位置：控制面板 → 任务计划 → 新增 → 计划的任务 → 用户定义的脚本
# 实际部署：任务名 xiaomi-camera-merge-task，运行账号 root
# 运行频率：每天 00:00 / 12:00 / 20:00 三次（由群晖任务计划分别调度，
#            本脚本本身只负责拉起重合并容器；容器内部会跳过已存在的输出，不会重复合并）
#
# 设计要点：
#   1. 本项目容器是【一次性任务】设计（compose 中 restart: "no"），
#      容器运行完会退出；本脚本负责定时拉起它。
#   2. 防重叠：若已有一个合并容器在运行（如积压很多时耗时较长），
#      本次调度直接跳过，避免两个进程同时读写同一批文件。
#   3. 清理残留：docker rm -f 清掉上一次退出后遗留的容器（如果存在），
#      再用 docker run --rm 启动新容器（--rm 运行完自动删除容器）。
# ============================================================================

# ---- 若已有合并容器在运行，则本次跳过（防止任务重叠） ----
if docker ps --format '{{.Names}}' | grep -qx 'xiaomi-camera-merge'; then
  exit 0
fi

# ---- 清理已退出/残留的容器（存在才删，失败静默忽略） ----
docker rm -f xiaomi-camera-merge >/dev/null 2>&1

# ---- 启动合并容器：一次性运行，挂载 CCTV，使用生产环境变量 ----
docker run --rm --name xiaomi-camera-merge \
  -e TZ=Asia/Shanghai \
  -e DELETE_SUCCESS=true \
  -e MAX_MERGE=0 \
  -e VIDEO_EXT=mp4 \
  -e MIN_AGE_MIN=30 \
  -e SKIP_CURRENT=true \
  -e GROUP_SIZE=10 \
  -e MIN_GROUP_SIZE=8 \
  -v /volume1/CCTV:/app/video \
  xiaomi-camera-merge:local

# ---- 结束提示（详细日志见 CCTV/xiaomi-video-merge-log/） ----
echo "[$(date '+%F %T')] 合并任务执行完毕（详见 /volume1/CCTV/xiaomi-video-merge-log/ 下的日志）"
