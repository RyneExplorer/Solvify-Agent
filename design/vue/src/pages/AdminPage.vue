<template>
  <div class="py-8 px-10">
    <h1 class="text-[28px] font-bold text-slate-900 mb-6" style="font-family: 'Space Grotesk', sans-serif; letter-spacing: -0.02em;">后台管理</h1>

    <!-- Tabs -->
    <div class="flex border-b border-slate-200 mb-6 overflow-x-auto">
      <button
        v-for="tab in adminTabs" :key="tab.key"
        @click="activeTab = tab.key"
        :class="[
          'px-4 py-2.5 text-sm border-b-2 transition-colors cursor-pointer whitespace-nowrap',
          activeTab === tab.key
            ? 'text-slate-900 font-medium border-slate-900'
            : 'text-slate-400 border-transparent hover:text-slate-600'
        ]"
      >
        {{ tab.label }}
      </button>
    </div>

    <!-- Users tab -->
    <div v-if="activeTab === 'users'">
      <div class="flex justify-between mb-4">
        <SearchInput v-model="userSearch" placeholder="搜索用户..." wrapper-class="w-80" />
        <AppButton>+ 添加用户</AppButton>
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">用户</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">邮箱</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">角色</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">最后登录</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(u, i) in users" :key="i" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3">
                <div class="flex items-center gap-2">
                  <AppAvatar :name="u.name" />
                  <span class="font-medium text-slate-900">{{ u.name }}</span>
                </div>
              </td>
              <td class="px-4 py-3 text-slate-900">{{ u.email }}</td>
              <td class="px-4 py-3"><AppBadge variant="blue">{{ u.role }}</AppBadge></td>
              <td class="px-4 py-3"><AppBadge :variant="u.status === 'active' ? 'success' : 'neutral'">{{ u.status === 'active' ? '活跃' : '停用' }}</AppBadge></td>
              <td class="px-4 py-3 text-slate-900">{{ u.lastLogin }}</td>
              <td class="px-4 py-3"><AppButton variant="ghost" size="sm">编辑</AppButton></td>
            </tr>
          </tbody>
        </table>
      </AppCard>
    </div>

    <!-- Sessions tab -->
    <div v-if="activeTab === 'sessions'">
      <AppCard>
        <h3 class="text-base font-semibold text-slate-900 mb-3" style="font-family: 'Space Grotesk', sans-serif;">会话管理</h3>
        <p class="text-sm text-slate-400 mb-4">查看、搜索、删除历史会话。会话记录保留 90 天，超过自动归档。</p>
        <div class="flex gap-2">
          <SearchInput v-model="sessionSearch" placeholder="搜索会话..." wrapper-class="flex-1" />
          <AppButton variant="danger" size="sm">清理过期会话</AppButton>
        </div>
      </AppCard>
    </div>

    <!-- KB tab -->
    <div v-if="activeTab === 'kb'">
      <AppCard>
        <h3 class="text-base font-semibold text-slate-900 mb-3" style="font-family: 'Space Grotesk', sans-serif;">知识库全局管理</h3>
        <p class="text-sm text-slate-400">查看所有租户的知识库状态，进行存储配额管理和异常排查。</p>
      </AppCard>
    </div>

    <!-- Logs tab -->
    <div v-if="activeTab === 'logs'">
      <div class="flex gap-2 mb-4">
        <AppSelect v-model="logLevel" class="w-32">
          <option>全部级别</option><option>ERROR</option><option>WARN</option><option>INFO</option>
        </AppSelect>
        <AppSelect v-model="logModule" class="w-32">
          <option>全部模块</option><option>Auth</option><option>KB</option><option>LLM</option><option>Search</option><option>Doc</option>
        </AppSelect>
        <SearchInput v-model="logSearch" placeholder="搜索日志..." wrapper-class="w-80" />
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">时间</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">级别</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">模块</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">消息</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(log, i) in logs" :key="i" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3 text-xs text-slate-900" style="font-family: 'JetBrains Mono', monospace;">{{ log.time }}</td>
              <td class="px-4 py-3">
                <AppBadge :variant="log.level === 'ERROR' ? 'error' : log.level === 'WARN' ? 'warning' : 'neutral'">
                  {{ log.level }}
                </AppBadge>
              </td>
              <td class="px-4 py-3 text-slate-900">{{ log.module }}</td>
              <td class="px-4 py-3 text-slate-900">{{ log.message }}</td>
            </tr>
          </tbody>
        </table>
      </AppCard>
    </div>

    <!-- Vector DB tab -->
    <div v-if="activeTab === 'vector'">
      <AppCard>
        <h3 class="text-base font-semibold text-slate-900 mb-2" style="font-family: 'Space Grotesk', sans-serif;">向量数据库配置</h3>
        <p class="text-[13px] text-slate-400 mb-4">配置系统使用的向量数据库引擎，修改后需重启服务生效。</p>
        <div class="mb-4">
          <label class="block text-[13px] font-medium text-slate-600 mb-1.5">引擎类型</label>
          <AppSelect v-model="vectorEngine" class="w-full">
            <option>PostgreSQL (pgvector)</option><option>Elasticsearch</option><option>Milvus</option><option>Weaviate</option>
          </AppSelect>
        </div>
        <div class="mb-5">
          <label class="block text-[13px] font-medium text-slate-600 mb-1.5">连接地址</label>
          <input placeholder="postgresql://localhost:5432/solvify" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-3 text-slate-900 outline-none transition-colors focus:border-slate-900" />
        </div>
        <AppButton class="mr-2">测试连接</AppButton>
        <AppButton variant="secondary">保存</AppButton>
      </AppCard>
    </div>

    <!-- Integration tab -->
    <div v-if="activeTab === 'integration'">
      <AppCard>
        <h3 class="text-base font-semibold text-slate-900 mb-2" style="font-family: 'Space Grotesk', sans-serif;">第三方平台集成</h3>
        <p class="text-[13px] text-slate-400 mb-4">配置钉钉、飞书、Notion 等平台的集成参数，实现知识库自动同步。</p>
        <div v-for="(item, i) in adminIntegrations" :key="item.name"
          class="flex justify-between items-center p-3.5 border border-slate-200 rounded-xl"
          :class="{ 'mb-2': i < adminIntegrations.length - 1 }"
        >
          <div>
            <div class="text-sm font-medium text-slate-900">{{ item.name }}</div>
            <div class="text-xs text-slate-400 mt-0.5">{{ item.desc }}</div>
          </div>
          <div class="flex items-center gap-2">
            <AppBadge :variant="item.status === '已连接' ? 'success' : 'neutral'">{{ item.status }}</AppBadge>
            <AppButton variant="secondary" size="sm">配置</AppButton>
          </div>
        </div>
      </AppCard>
    </div>

    <!-- Config tab -->
    <div v-if="activeTab === 'config'">
      <AppCard>
        <h3 class="text-base font-semibold text-slate-900 mb-3" style="font-family: 'Space Grotesk', sans-serif;">全局配置管理</h3>
        <p class="text-sm text-slate-400">管理全局系统配置参数，包括存储配额、调用限制等。</p>
      </AppCard>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import AppCard from '../components/ui/AppCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import AppBadge from '../components/ui/AppBadge.vue'
import AppAvatar from '../components/ui/AppAvatar.vue'
import AppSelect from '../components/ui/AppSelect.vue'
import SearchInput from '../components/ui/SearchInput.vue'

const activeTab = ref('users')
const userSearch = ref('')
const sessionSearch = ref('')
const logLevel = ref('全部级别')
const logModule = ref('全部模块')
const logSearch = ref('')
const vectorEngine = ref('PostgreSQL (pgvector)')

const adminTabs = [
  { key: 'users', label: '用户管理' },
  { key: 'sessions', label: '会话管理' },
  { key: 'kb', label: '知识库管理' },
  { key: 'logs', label: '系统日志' },
  { key: 'vector', label: '向量数据库' },
  { key: 'integration', label: '平台集成' },
  { key: 'config', label: '配置管理' },
]

const users = [
  { name: '张三', email: 'zhangsan@company.com', role: '超级管理员', status: 'active', lastLogin: '在线' },
  { name: '李四', email: 'lisi@company.com', role: '管理员', status: 'active', lastLogin: '2 小时前' },
  { name: '王五', email: 'wangwu@company.com', role: '编辑者', status: 'active', lastLogin: '1 天前' },
  { name: '赵六', email: 'zhaoliu@company.com', role: '观察者', status: 'inactive', lastLogin: '1 周前' },
  { name: '孙七', email: 'sunqi@company.com', role: '编辑者', status: 'active', lastLogin: '3 天前' },
]

const logs = [
  { time: '2024-01-15 14:32:01', level: 'INFO', module: 'Auth', message: '用户 zhangsan 登录成功' },
  { time: '2024-01-15 14:30:15', level: 'INFO', module: 'KB', message: '知识库「技术文档库」新增 5 篇文档' },
  { time: '2024-01-15 14:28:44', level: 'WARN', module: 'LLM', message: 'GPT-4 API 调用超时，已降级到 GPT-3.5' },
  { time: '2024-01-15 14:25:10', level: 'ERROR', module: 'Search', message: '联网搜索 API 返回 429 错误' },
  { time: '2024-01-15 14:20:00', level: 'INFO', module: 'Doc', message: '文档「架构设计.pdf」处理完成' },
]

const adminIntegrations = [
  { name: '钉钉', desc: '通过 Webhook 同步知识库（需企业认证）', status: '已连接' },
  { name: '飞书', desc: '从飞书文档同步知识库（OAuth 授权）', status: '已连接' },
  { name: 'Notion', desc: '从 Notion 页面同步知识库（API Key）', status: '未配置' },
]
</script>
