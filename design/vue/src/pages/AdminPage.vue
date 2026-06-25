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
        <SearchInput v-model="userSearch" placeholder="搜索用户名/邮箱..." wrapper-class="w-80" />
        <AppButton @click="openUserModal()">+ 添加用户</AppButton>
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">用户</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">邮箱</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">角色</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="u in users" :key="u.id" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3">
                <div class="flex items-center gap-2">
                  <AppAvatar :name="u.username" />
                  <span class="font-medium text-slate-900">{{ u.username }}</span>
                </div>
              </td>
              <td class="px-4 py-3 text-slate-900">{{ u.email }}</td>
              <td class="px-4 py-3"><AppBadge variant="blue">{{ roleText(u.role) }}</AppBadge></td>
              <td class="px-4 py-3"><AppBadge :variant="u.status === 1 ? 'success' : 'neutral'">{{ u.status === 1 ? '活跃' : '停用' }}</AppBadge></td>
              <td class="px-4 py-3">
                <div class="flex items-center gap-1">
                  <AppButton variant="ghost" size="sm" @click="openUserModal(u)">编辑</AppButton>
                  <AppButton variant="ghost" size="sm" @click="openResetPasswordModal(u)">重置密码</AppButton>
                  <AppButton variant="ghost" size="sm" class="text-red-600 hover:text-red-700 hover:bg-red-50" @click="deleteUser(u.id)">删除</AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </AppCard>
    </div>

    <!-- Sessions tab -->
    <div v-if="activeTab === 'sessions'">
      <div class="flex justify-between mb-4">
        <div class="flex gap-2">
          <SearchInput v-model="sessionSearch" placeholder="搜索标题/用户名..." wrapper-class="w-64" />
          <AppSelect v-model="sessionStatus" :options="sessionStatusOptions" />
        </div>
        <AppButton variant="danger" size="sm" @click="cleanupSessions">清理过期会话</AppButton>
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">标题</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">用户</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">模型</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">消息数</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">更新时间</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="s in sessions" :key="s.id" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3 font-medium text-slate-900 max-w-xs truncate" :title="s.title">{{ s.title || '未命名会话' }}</td>
              <td class="px-4 py-3 text-slate-900">{{ s.username }}</td>
              <td class="px-4 py-3 text-slate-500">{{ s.model_id }}</td>
              <td class="px-4 py-3"><AppBadge :variant="s.status === 'active' ? 'success' : 'neutral'">{{ s.status }}</AppBadge></td>
              <td class="px-4 py-3 text-slate-500">{{ s.message_count }}</td>
              <td class="px-4 py-3 text-slate-500">{{ formatDate(s.updated_at) }}</td>
              <td class="px-4 py-3">
                <AppButton variant="ghost" size="sm" class="text-red-600 hover:text-red-700 hover:bg-red-50" @click="deleteSession(s.id)">删除</AppButton>
              </td>
            </tr>
          </tbody>
        </table>
        <div v-if="!sessions.length" class="px-4 py-8 text-center text-sm text-slate-400">暂无会话</div>
      </AppCard>
      <div v-if="sessionTotal > sessionPageSize" class="flex justify-end mt-4">
        <el-pagination v-model:current-page="sessionPage" :page-size="sessionPageSize" :total="sessionTotal" layout="prev, pager, next" background />
      </div>
    </div>

    <!-- Models tab -->
    <div v-if="activeTab === 'models'">
      <div class="flex justify-between mb-4">
        <SearchInput v-model="modelSearch" placeholder="搜索模型..." wrapper-class="w-80" />
        <AppButton @click="openModelModal()">+ 添加模型</AppButton>
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">提供商</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">模型 ID</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">Base URL</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="m in filteredModels" :key="m.id" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3 font-medium text-slate-900">{{ m.provider }}</td>
              <td class="px-4 py-3 text-slate-900">{{ m.model_id }}</td>
              <td class="px-4 py-3 text-slate-900">{{ m.base_url || '-' }}</td>
              <td class="px-4 py-3"><AppBadge :variant="m.is_enabled ? 'success' : 'neutral'">{{ m.is_enabled ? '启用' : '停用' }}</AppBadge></td>
              <td class="px-4 py-3">
                <div class="flex items-center gap-1">
                  <AppButton variant="ghost" size="sm" @click="openModelModal(m)">编辑</AppButton>
                  <AppButton variant="ghost" size="sm" @click="toggleModelEnabled(m)">{{ m.is_enabled ? '停用' : '启用' }}</AppButton>
                  <AppButton variant="ghost" size="sm" class="text-red-600 hover:text-red-700 hover:bg-red-50" @click="deleteModel(m.id)">删除</AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </AppCard>
    </div>

    <!-- Tools tab -->
    <div v-if="activeTab === 'tools'">
      <div class="flex justify-between mb-4">
        <SearchInput v-model="toolSearch" placeholder="搜索工具类型..." wrapper-class="w-80" />
        <AppButton @click="openToolTypeModal()">+ 添加工具类型</AppButton>
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">名称</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">Key</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">执行模式</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">供应商数</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="t in filteredToolTypes" :key="t.id" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3 font-medium text-slate-900">{{ t.name }}</td>
              <td class="px-4 py-3 text-slate-500 font-mono text-xs">{{ t.tool_key }}</td>
              <td class="px-4 py-3 text-slate-900">{{ t.execution_mode }}</td>
              <td class="px-4 py-3 text-slate-900">{{ t.provider_count }}</td>
              <td class="px-4 py-3"><AppBadge :variant="t.is_enabled ? 'success' : 'neutral'">{{ t.is_enabled ? '启用' : '停用' }}</AppBadge></td>
              <td class="px-4 py-3">
                <div class="flex items-center gap-1">
                  <AppButton variant="ghost" size="sm" @click="openToolTypeModal(t)">编辑</AppButton>
                  <AppButton variant="ghost" size="sm" @click="toggleToolTypeEnabled(t)">{{ t.is_enabled ? '停用' : '启用' }}</AppButton>
                  <AppButton variant="ghost" size="sm" @click="viewToolProviders(t)">供应商</AppButton>
                  <AppButton variant="ghost" size="sm" class="text-red-600 hover:text-red-700 hover:bg-red-50" @click="deleteToolType(t.id)">删除</AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </AppCard>
    </div>

    <!-- Tool Providers tab -->
    <div v-if="activeTab === 'toolProviders'">
      <div class="flex justify-between mb-4">
        <div class="flex items-center gap-2">
          <AppButton variant="secondary" size="sm" @click="activeTab = 'tools'">← 返回工具类型</AppButton>
          <span class="text-sm text-slate-500">{{ currentToolType?.name }} 的供应商</span>
        </div>
        <AppButton @click="openToolProviderModal()">+ 添加供应商</AppButton>
      </div>
      <AppCard class="!p-0 overflow-hidden">
        <table class="w-full text-sm border-collapse">
          <thead>
            <tr class="bg-slate-50 border-b border-slate-200">
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">名称</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">Provider Key</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">描述</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">状态</th>
              <th class="text-left uppercase tracking-wider text-xs font-medium text-slate-400 px-4 py-3">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="p in toolProviders" :key="p.id" class="border-b border-slate-100 last:border-b-0">
              <td class="px-4 py-3 font-medium text-slate-900">{{ p.name }}</td>
              <td class="px-4 py-3 text-slate-900">{{ p.provider_key }}</td>
              <td class="px-4 py-3 text-slate-900">{{ p.description || '-' }}</td>
              <td class="px-4 py-3"><AppBadge :variant="p.is_enabled ? 'success' : 'neutral'">{{ p.is_enabled ? '启用' : '停用' }}</AppBadge></td>
              <td class="px-4 py-3">
                <div class="flex items-center gap-1">
                  <AppButton variant="ghost" size="sm" @click="openToolProviderModal(p)">编辑</AppButton>
                  <AppButton variant="ghost" size="sm" @click="toggleToolProviderEnabled(p)">{{ p.is_enabled ? '停用' : '启用' }}</AppButton>
                  <AppButton variant="ghost" size="sm" class="text-red-600 hover:text-red-700 hover:bg-red-50" @click="deleteToolProvider(p.id)">删除</AppButton>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </AppCard>
    </div>

    <!-- Model Modal -->
    <div v-if="modelModalVisible" class="fixed inset-0 z-50 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/30" @click="modelModalVisible = false" />
      <div class="relative bg-white rounded-2xl shadow-xl border border-slate-200 p-6 w-[480px]">
        <h3 class="text-lg font-semibold text-slate-900 mb-4">{{ editingModel ? '编辑模型' : '添加模型' }}</h3>
        <div class="space-y-4">
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">提供商</label>
            <input v-model="modelForm.provider" placeholder="例如：OpenAI / 阿里云 / 智谱" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">模型 ID</label>
            <input v-model="modelForm.model_id" placeholder="例如：gpt-4 / qwen-max" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">Base URL</label>
            <input v-model="modelForm.base_url" placeholder="https://api.openai.com/v1" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">API Key</label>
            <input v-model="modelForm.api_key" type="password" placeholder="sk-..." class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
        </div>
        <div class="flex justify-end gap-2 mt-6">
          <AppButton variant="secondary" @click="modelModalVisible = false">取消</AppButton>
          <AppButton :disabled="!modelFormValid" @click="saveModel">保存</AppButton>
        </div>
      </div>
    </div>

    <!-- Tool Type Modal -->
    <div v-if="toolTypeModalVisible" class="fixed inset-0 z-50 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/30" @click="toolTypeModalVisible = false" />
      <div class="relative bg-white rounded-2xl shadow-xl border border-slate-200 p-6 w-[480px]">
        <h3 class="text-lg font-semibold text-slate-900 mb-4">{{ editingToolType ? '编辑工具类型' : '添加工具类型' }}</h3>
        <div class="space-y-4">
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">名称</label>
            <input v-model="toolTypeForm.name" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">Tool Key</label>
            <input v-model="toolTypeForm.tool_key" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">执行模式</label>
            <AppSelect v-model="toolTypeForm.execution_mode" class="w-full">
              <el-option value="sync" label="同步" />
              <el-option value="async" label="异步" />
            </AppSelect>
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">描述</label>
            <textarea v-model="toolTypeForm.description" rows="2" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900 resize-none" />
          </div>
        </div>
        <div class="flex justify-end gap-2 mt-6">
          <AppButton variant="secondary" @click="toolTypeModalVisible = false">取消</AppButton>
          <AppButton :disabled="!toolTypeFormValid" @click="saveToolType">保存</AppButton>
        </div>
      </div>
    </div>

    <!-- Tool Provider Modal -->
    <div v-if="toolProviderModalVisible" class="fixed inset-0 z-50 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/30" @click="toolProviderModalVisible = false" />
      <div class="relative bg-white rounded-2xl shadow-xl border border-slate-200 p-6 w-[520px]" style="max-height:90vh;overflow-y:auto;">
        <h3 class="text-lg font-semibold text-slate-900 mb-4">{{ editingToolProvider ? '编辑供应商' : '添加供应商' }}</h3>
        <div class="space-y-4">
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">名称 <span class="text-red-500">*</span></label>
            <input v-model="toolProviderForm.name" placeholder="博查 AI" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">Provider Key <span class="text-red-500">*</span></label>
            <input v-model="toolProviderForm.provider_key" placeholder="bocha" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">供应商类型 <span class="text-red-500">*</span></label>
            <AppSelect v-model="toolProviderForm.provider_type" class="w-full">
              <el-option value="http" label="HTTP" />
              <el-option value="mcp" label="MCP" />
              <el-option value="custom" label="自定义" />
            </AppSelect>
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">描述</label>
            <textarea v-model="toolProviderForm.description" rows="2" placeholder="供应商描述" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900 resize-none" />
          </div>

          <!-- HTTP Config -->
          <div v-if="toolProviderForm.provider_type === 'http'">
            <div class="flex items-center justify-between mb-1.5">
              <label class="block text-[13px] font-medium text-slate-600">HTTP 配置 <span class="text-red-500">*</span></label>
            </div>
            <div class="space-y-3 border border-slate-200 rounded-xl p-3 bg-slate-50">
              <div class="grid grid-cols-3 gap-2">
                <AppSelect v-model="toolProviderForm.method" class="w-full">
                  <el-option value="GET" label="GET" />
                  <el-option value="POST" label="POST" />
                </AppSelect>
                <input v-model="toolProviderForm.url" placeholder="https://api.example.com/search" class="col-span-2 w-full rounded-xl border border-slate-200 bg-white text-sm px-3 py-2 text-slate-900 outline-none focus:border-slate-900" />
              </div>
              <div>
                <div class="flex items-center justify-between mb-1">
                  <label class="text-xs font-medium text-slate-500">Headers</label>
                  <button type="button" @click="addKV(toolProviderForm.headers_rows)" class="text-xs text-slate-600 hover:text-slate-900 border border-slate-200 px-2 py-0.5 rounded-md hover:bg-slate-50">+ 添加</button>
                </div>
                <div v-if="!toolProviderForm.headers_rows.length" class="text-xs text-slate-400 px-3 py-2 bg-white rounded-lg border border-slate-200 border-dashed text-center">暂无</div>
                <div v-for="(row, idx) in toolProviderForm.headers_rows" :key="idx" class="flex items-center gap-2 mb-2">
                  <input v-model="row.key" placeholder="key" class="flex-1 rounded-lg border border-slate-200 bg-white text-xs px-3 py-2 outline-none focus:border-slate-900" />
                  <input v-model="row.value" placeholder="value" class="flex-1 rounded-lg border border-slate-200 bg-white text-xs px-3 py-2 outline-none focus:border-slate-900" />
                  <button type="button" @click="removeKV(toolProviderForm.headers_rows, idx)" class="text-xs text-red-600 hover:text-red-700 px-1">删除</button>
                </div>
              </div>
              <div>
                <label class="block text-xs font-medium text-slate-500 mb-1">请求体模板 (JSON)</label>
                <textarea v-model="toolProviderForm.body_template_text" rows="3" placeholder='{"query": "{{query}}"}' class="w-full rounded-xl border border-slate-200 bg-white text-xs px-3 py-2 text-slate-900 outline-none focus:border-slate-900 resize-none font-mono"></textarea>
              </div>
              <div>
                <div class="flex items-center justify-between mb-1">
                  <label class="text-xs font-medium text-slate-500">响应映射 (JSON Path)</label>
                  <button type="button" @click="addKV(toolProviderForm.response_mapping_rows)" class="text-xs text-slate-600 hover:text-slate-900 border border-slate-200 px-2 py-0.5 rounded-md hover:bg-slate-50">+ 添加</button>
                </div>
                <div v-if="!toolProviderForm.response_mapping_rows.length" class="text-xs text-slate-400 px-3 py-2 bg-white rounded-lg border border-slate-200 border-dashed text-center">暂无</div>
                <div v-for="(row, idx) in toolProviderForm.response_mapping_rows" :key="idx" class="flex items-center gap-2 mb-2">
                  <input v-model="row.key" placeholder="results" class="flex-1 rounded-lg border border-slate-200 bg-white text-xs px-3 py-2 outline-none focus:border-slate-900" />
                  <input v-model="row.value" placeholder="$.data.results" class="flex-1 rounded-lg border border-slate-200 bg-white text-xs px-3 py-2 outline-none focus:border-slate-900" />
                  <button type="button" @click="removeKV(toolProviderForm.response_mapping_rows, idx)" class="text-xs text-red-600 hover:text-red-700 px-1">删除</button>
                </div>
              </div>
            </div>
          </div>

          <!-- Config Schema Builder -->
          <div>
            <div class="flex items-center justify-between mb-1.5">
              <label class="block text-[13px] font-medium text-slate-600">用户配置 Schema</label>
              <button type="button" @click="addSchemaField" class="text-xs text-slate-600 hover:text-slate-900 border border-slate-200 px-2 py-1 rounded-md hover:bg-slate-50">+ 添加字段</button>
            </div>
            <p class="text-xs text-slate-400 mb-2">定义用户需要填写的配置项</p>
            <div v-if="!toolProviderForm.schema_fields.length" class="text-xs text-slate-400 px-3 py-4 bg-slate-50 rounded-xl border border-slate-200 border-dashed text-center">暂无字段</div>
            <div v-for="(f, idx) in toolProviderForm.schema_fields" :key="idx" class="mb-3 border border-slate-200 rounded-xl p-3 bg-slate-50">
              <div class="grid grid-cols-2 gap-2 mb-2">
                <input v-model="f.key" placeholder="字段 key，如 api_key" class="w-full rounded-lg border border-slate-200 bg-white text-xs px-3 py-2 outline-none focus:border-slate-900" />
                <input v-model="f.title" placeholder="显示标题" class="w-full rounded-lg border border-slate-200 bg-white text-xs px-3 py-2 outline-none focus:border-slate-900" />
              </div>
              <div class="grid grid-cols-2 gap-2 mb-2">
                <select v-model="f.type" class="w-full rounded-lg border border-slate-200 bg-white text-xs px-3 py-2 outline-none focus:border-slate-900">
                  <option value="string">字符串</option>
                  <option value="integer">整数</option>
                  <option value="number">小数</option>
                  <option value="boolean">布尔</option>
                </select>
                <input v-model="f.default_value" placeholder="默认值（可选）" class="w-full rounded-lg border border-slate-200 bg-white text-xs px-3 py-2 outline-none focus:border-slate-900" />
              </div>
              <input v-model="f.description" placeholder="字段描述（可选）" class="w-full rounded-lg border border-slate-200 bg-white text-xs px-3 py-2 mb-2 outline-none focus:border-slate-900" />
              <div class="flex flex-wrap items-center gap-3">
                <label class="flex items-center gap-1 text-xs text-slate-600 cursor-pointer"><input type="checkbox" v-model="f.required" class="w-3.5 h-3.5 rounded border-slate-300" /> 必填</label>
                <label v-if="f.type === 'string'" class="flex items-center gap-1 text-xs text-slate-600 cursor-pointer"><input type="checkbox" v-model="f.secret" class="w-3.5 h-3.5 rounded border-slate-300" /> 密码</label>
                <button type="button" @click="removeSchemaField(idx)" class="text-xs text-red-600 hover:text-red-700 ml-auto">删除</button>
              </div>
            </div>
          </div>

          <!-- Admin Config Key-Value -->
          <div>
            <div class="flex items-center justify-between mb-1.5">
              <label class="block text-[13px] font-medium text-slate-600">管理员业务参数</label>
              <button type="button" @click="addKV(toolProviderForm.admin_config_rows)" class="text-xs text-slate-600 hover:text-slate-900 border border-slate-200 px-2 py-1 rounded-md hover:bg-slate-50">+ 添加</button>
            </div>
            <div v-if="!toolProviderForm.admin_config_rows.length" class="text-xs text-slate-400 px-3 py-4 bg-slate-50 rounded-xl border border-slate-200 border-dashed text-center">暂无参数</div>
            <div v-for="(row, idx) in toolProviderForm.admin_config_rows" :key="idx" class="flex items-center gap-2 mb-2">
              <input v-model="row.key" placeholder="key" class="flex-1 rounded-lg border border-slate-200 bg-slate-50 text-xs px-3 py-2 outline-none focus:border-slate-900" />
              <input v-model="row.value" placeholder="value" class="flex-1 rounded-lg border border-slate-200 bg-slate-50 text-xs px-3 py-2 outline-none focus:border-slate-900" />
              <button type="button" @click="removeKV(toolProviderForm.admin_config_rows, idx)" class="text-xs text-red-600 hover:text-red-700 px-1">删除</button>
            </div>
          </div>

          <!-- Rate Limit Key-Value -->
          <div>
            <div class="flex items-center justify-between mb-1.5">
              <label class="block text-[13px] font-medium text-slate-600">限流配置</label>
              <button type="button" @click="addKV(toolProviderForm.rate_limit_rows)" class="text-xs text-slate-600 hover:text-slate-900 border border-slate-200 px-2 py-1 rounded-md hover:bg-slate-50">+ 添加</button>
            </div>
            <div v-if="!toolProviderForm.rate_limit_rows.length" class="text-xs text-slate-400 px-3 py-4 bg-slate-50 rounded-xl border border-slate-200 border-dashed text-center">暂无参数</div>
            <div v-for="(row, idx) in toolProviderForm.rate_limit_rows" :key="idx" class="flex items-center gap-2 mb-2">
              <input v-model="row.key" placeholder="key" class="flex-1 rounded-lg border border-slate-200 bg-slate-50 text-xs px-3 py-2 outline-none focus:border-slate-900" />
              <input v-model="row.value" placeholder="value" class="flex-1 rounded-lg border border-slate-200 bg-slate-50 text-xs px-3 py-2 outline-none focus:border-slate-900" />
              <button type="button" @click="removeKV(toolProviderForm.rate_limit_rows, idx)" class="text-xs text-red-600 hover:text-red-700 px-1">删除</button>
            </div>
          </div>
        </div>
        <div class="flex justify-end gap-2 mt-6">
          <AppButton variant="secondary" @click="toolProviderModalVisible = false">取消</AppButton>
          <AppButton :disabled="!toolProviderFormValid" @click="saveToolProvider">保存</AppButton>
        </div>
      </div>
    </div>

    <!-- User Modal -->
    <div v-if="userModalVisible" class="fixed inset-0 z-50 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/30" @click="userModalVisible = false" />
      <div class="relative bg-white rounded-2xl shadow-xl border border-slate-200 p-6 w-[480px]">
        <h3 class="text-lg font-semibold text-slate-900 mb-4">{{ editingUser ? '编辑用户' : '添加用户' }}</h3>
        <div class="space-y-4">
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">用户名 <span class="text-red-500">*</span></label>
            <input v-model="userForm.username" placeholder="请输入用户名" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">邮箱 <span class="text-red-500">*</span></label>
            <input v-model="userForm.email" placeholder="请输入邮箱" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div v-if="!editingUser">
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">密码 <span class="text-red-500">*</span></label>
            <input v-model="userForm.password" type="password" placeholder="至少 6 位" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">角色</label>
            <AppSelect v-model="userForm.role" class="w-full">
              <el-option :value="1" label="普通用户" />
              <el-option :value="2" label="管理员" />
            </AppSelect>
          </div>
          <div>
            <label class="block text-[13px] font-medium text-slate-600 mb-1.5">状态</label>
            <AppSelect v-model="userForm.status" class="w-full">
              <el-option :value="1" label="正常" />
              <el-option :value="2" label="禁用" />
              <el-option :value="3" label="注销" />
              <el-option :value="4" label="待验证" />
            </AppSelect>
          </div>
        </div>
        <div class="flex justify-end gap-2 mt-6">
          <AppButton variant="secondary" @click="userModalVisible = false">取消</AppButton>
          <AppButton :disabled="!userFormValid" @click="saveUser">保存</AppButton>
        </div>
      </div>
    </div>

    <!-- Reset Password Modal -->
    <div v-if="resetPasswordModalVisible" class="fixed inset-0 z-50 flex items-center justify-center">
      <div class="absolute inset-0 bg-black/30" @click="resetPasswordModalVisible = false" />
      <div class="relative bg-white rounded-2xl shadow-xl border border-slate-200 p-6 w-[400px]">
        <h3 class="text-lg font-semibold text-slate-900 mb-4">重置密码</h3>
        <p class="text-sm text-slate-500 mb-4">正在为 <span class="font-medium text-slate-900">{{ resetPasswordUser?.username }}</span> 设置新密码</p>
        <div>
          <label class="block text-[13px] font-medium text-slate-600 mb-1.5">新密码 <span class="text-red-500">*</span></label>
          <input v-model="resetPasswordForm.password" type="password" placeholder="至少 6 位" class="w-full rounded-xl border border-slate-200 bg-slate-50 text-sm px-4 py-2.5 text-slate-900 outline-none focus:border-slate-900" />
        </div>
        <div class="flex justify-end gap-2 mt-6">
          <AppButton variant="secondary" @click="resetPasswordModalVisible = false">取消</AppButton>
          <AppButton :disabled="!resetPasswordFormValid" @click="saveResetPassword">保存</AppButton>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import AppCard from '../components/ui/AppCard.vue'
import AppButton from '../components/ui/AppButton.vue'
import AppBadge from '../components/ui/AppBadge.vue'
import AppAvatar from '../components/ui/AppAvatar.vue'
import AppSelect from '../components/ui/AppSelect.vue'
import SearchInput from '../components/ui/SearchInput.vue'
import type { ModelInfo } from '@/types/model'
import type { ToolTypeInfo, ToolProviderInfo } from '@/types/tool'
import type { AdminSession } from '@/types/chat'
import {
  adminListUsers,
  adminCreateUser,
  adminUpdateUser,
  adminDeleteUser,
  adminResetUserPassword,
  adminListModels,
  adminCreateModel,
  adminUpdateModel,
  adminDeleteModel,
  adminListToolTypes,
  adminCreateToolType,
  adminUpdateToolType,
  adminDeleteToolType,
  adminListToolProviders,
  adminCreateToolProvider,
  adminUpdateToolProvider,
  adminDeleteToolProvider,
  adminListSessions,
  adminDeleteSession,
  adminCleanupSessions,
} from '@/api/admin'
import type { AdminUser } from '@/types/auth'

const activeTab = ref('users')
const userSearch = ref('')
const sessionSearch = ref('')
const sessionStatus = ref('')
const sessionPage = ref(1)
const sessionPageSize = ref(10)
const sessionTotal = ref(0)
const modelSearch = ref('')
const toolSearch = ref('')

const adminTabs = [
  { key: 'users', label: '用户管理' },
  { key: 'sessions', label: '会话管理' },
  { key: 'models', label: '模型管理' },
  { key: 'tools', label: '工具管理' },
]

const users = ref<AdminUser[]>([])
const sessions = ref<AdminSession[]>([])

const sessionStatusOptions = [
  { value: '', label: '全部状态' },
  { value: 'active', label: '活跃' },
  { value: 'archived', label: '已归档' },
  { value: 'closed', label: '已关闭' },
]

// ── Models ──
const models = ref<ModelInfo[]>([])
const filteredModels = computed(() => {
  if (!modelSearch.value.trim()) return models.value
  const kw = modelSearch.value.toLowerCase()
  return models.value.filter(
    m => m.provider.toLowerCase().includes(kw) || m.model_id.toLowerCase().includes(kw)
  )
})
const modelModalVisible = ref(false)
const editingModel = ref<ModelInfo | null>(null)
const modelForm = ref({ provider: '', model_id: '', base_url: '', api_key: '' })
const modelFormValid = computed(() => modelForm.value.provider.trim() && modelForm.value.model_id.trim())

function openModelModal(model?: ModelInfo) {
  editingModel.value = model ?? null
  if (model) {
    modelForm.value = { provider: model.provider, model_id: model.model_id, base_url: model.base_url || '', api_key: '' }
  } else {
    modelForm.value = { provider: '', model_id: '', base_url: '', api_key: '' }
  }
  modelModalVisible.value = true
}

async function saveModel() {
  try {
    if (editingModel.value) {
      const payload: Partial<typeof modelForm.value> = { ...modelForm.value }
      if (!payload.api_key) delete payload.api_key
      await adminUpdateModel(editingModel.value.id, payload)
    } else {
      await adminCreateModel(modelForm.value)
    }
    modelModalVisible.value = false
    await loadModels()
    ElMessage.success(editingModel.value ? '保存成功' : '添加成功')
  } catch (e: any) {
    ElMessage.error(e.message || '保存失败')
  }
}

async function toggleModelEnabled(model: ModelInfo) {
  try {
    await adminUpdateModel(model.id, { is_enabled: !model.is_enabled })
    await loadModels()
    ElMessage.success(model.is_enabled ? '停用成功' : '启用成功')
  } catch (e: any) {
    ElMessage.error(e.message || '操作失败')
  }
}

async function deleteModel(id: string) {
  try {
    await ElMessageBox.confirm('确定删除这个模型吗？', '提示', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    await adminDeleteModel(id)
    await loadModels()
    ElMessage.success('删除成功')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '删除失败')
  }
}

async function loadModels() {
  try {
    const res = await adminListModels()
    if (res.code === 0) models.value = res.data.models || []
  } catch (e: any) {
    ElMessage.error(e.message || '加载模型失败')
  }
}

// ── Users ──
const userModalVisible = ref(false)
const editingUser = ref<AdminUser | null>(null)
const userForm = ref({ username: '', email: '', password: '', status: 1, role: 1 })
const userFormValid = computed(() => {
  if (editingUser.value) {
    return userForm.value.username.trim() && userForm.value.email.trim()
  }
  return userForm.value.username.trim() && userForm.value.email.trim() && userForm.value.password.length >= 6
})

function roleText(role: number) {
  const map: Record<number, string> = { 1: '普通用户', 2: '管理员' }
  return map[role] || '未知'
}

function openUserModal(user?: AdminUser) {
  editingUser.value = user ?? null
  if (user) {
    userForm.value = { username: user.username, email: user.email, password: '', status: user.status, role: user.role }
  } else {
    userForm.value = { username: '', email: '', password: '', status: 1, role: 1 }
  }
  userModalVisible.value = true
}

async function saveUser() {
  try {
    if (editingUser.value) {
      const payload: Partial<typeof userForm.value> = { ...userForm.value }
      if (!payload.password) delete payload.password
      await adminUpdateUser(editingUser.value.id, payload)
    } else {
      await adminCreateUser(userForm.value)
    }
    userModalVisible.value = false
    await loadUsers()
    ElMessage.success(editingUser.value ? '保存成功' : '添加成功')
  } catch (e: any) {
    ElMessage.error(e.message || '保存失败')
  }
}

async function deleteUser(id: string) {
  try {
    await ElMessageBox.confirm('确定删除这个用户吗？', '提示', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    await adminDeleteUser(id)
    await loadUsers()
    ElMessage.success('删除成功')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '删除失败')
  }
}

async function loadUsers() {
  try {
    const res = await adminListUsers({ page: 1, pageSize: 100, username: userSearch.value, email: userSearch.value })
    if (res.code === 0) users.value = res.data.list || []
  } catch (e: any) {
    ElMessage.error(e.message || '加载用户失败')
  }
}

// ── Sessions ──
function formatDate(date: string) {
  if (!date) return '-'
  return new Date(date).toLocaleString('zh-CN', { hour12: false })
}

async function loadSessions() {
  try {
    const res = await adminListSessions({
      page: sessionPage.value,
      pageSize: sessionPageSize.value,
      keyword: sessionSearch.value,
      status: sessionStatus.value,
    })
    if (res.code === 0) {
      sessions.value = res.data.list || []
      sessionTotal.value = res.data.total || 0
    }
  } catch (e: any) {
    ElMessage.error(e.message || '加载会话失败')
  }
}

async function deleteSession(id: string) {
  try {
    await ElMessageBox.confirm('确定删除这个会话吗？', '提示', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    await adminDeleteSession(id)
    await loadSessions()
    ElMessage.success('删除成功')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '删除失败')
  }
}

async function cleanupSessions() {
  try {
    await ElMessageBox.confirm('确定清理所有过期会话吗？此操作不可恢复。', '提示', { confirmButtonText: '清理', cancelButtonText: '取消', type: 'warning' })
    const res = await adminCleanupSessions()
    if (res.code === 0) {
      await loadSessions()
      ElMessage.success(`已清理 ${res.data.deleted} 个过期会话`)
    }
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '清理失败')
  }
}

// Reset Password
const resetPasswordModalVisible = ref(false)
const resetPasswordUser = ref<AdminUser | null>(null)
const resetPasswordForm = ref({ password: '' })
const resetPasswordFormValid = computed(() => resetPasswordForm.value.password.length >= 6)

function openResetPasswordModal(user: AdminUser) {
  resetPasswordUser.value = user
  resetPasswordForm.value = { password: '' }
  resetPasswordModalVisible.value = true
}

async function saveResetPassword() {
  if (!resetPasswordUser.value) return
  try {
    await adminResetUserPassword(resetPasswordUser.value.id, resetPasswordForm.value)
    resetPasswordModalVisible.value = false
    ElMessage.success('重置密码成功')
  } catch (e: any) {
    ElMessage.error(e.message || '重置密码失败')
  }
}

// ── Tool Types ──
const toolTypes = ref<ToolTypeInfo[]>([])
const filteredToolTypes = computed(() => {
  if (!toolSearch.value.trim()) return toolTypes.value
  const kw = toolSearch.value.toLowerCase()
  return toolTypes.value.filter(t => t.name.toLowerCase().includes(kw) || t.tool_key.toLowerCase().includes(kw))
})
const toolTypeModalVisible = ref(false)
const editingToolType = ref<ToolTypeInfo | null>(null)
const toolTypeForm = ref({ name: '', tool_key: '', description: '', execution_mode: 'sync' })
const toolTypeFormValid = computed(() => toolTypeForm.value.name.trim() && toolTypeForm.value.tool_key.trim())

function openToolTypeModal(toolType?: ToolTypeInfo) {
  editingToolType.value = toolType ?? null
  if (toolType) {
    toolTypeForm.value = { name: toolType.name, tool_key: toolType.tool_key, description: toolType.description, execution_mode: toolType.execution_mode }
  } else {
    toolTypeForm.value = { name: '', tool_key: '', description: '', execution_mode: 'sync' }
  }
  toolTypeModalVisible.value = true
}

async function saveToolType() {
  try {
    if (editingToolType.value) {
      await adminUpdateToolType(editingToolType.value.id, toolTypeForm.value)
    } else {
      await adminCreateToolType(toolTypeForm.value)
    }
    toolTypeModalVisible.value = false
    await loadToolTypes()
    ElMessage.success(editingToolType.value ? '保存成功' : '添加成功')
  } catch (e: any) {
    ElMessage.error(e.message || '保存失败')
  }
}

async function toggleToolTypeEnabled(toolType: ToolTypeInfo) {
  try {
    await adminUpdateToolType(toolType.id, { is_enabled: !toolType.is_enabled })
    await loadToolTypes()
    ElMessage.success(toolType.is_enabled ? '停用成功' : '启用成功')
  } catch (e: any) {
    ElMessage.error(e.message || '操作失败')
  }
}

async function deleteToolType(id: string) {
  try {
    await ElMessageBox.confirm('确定删除这个工具类型吗？', '提示', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    await adminDeleteToolType(id)
    await loadToolTypes()
    ElMessage.success('删除成功')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '删除失败')
  }
}

async function loadToolTypes() {
  try {
    const res = await adminListToolTypes()
    if (res.code === 0) toolTypes.value = res.data.tool_types || []
  } catch (e: any) {
    ElMessage.error(e.message || '加载工具类型失败')
  }
}

// ── Tool Providers ──
const currentToolType = ref<ToolTypeInfo | null>(null)
const toolProviders = ref<ToolProviderInfo[]>([])
const toolProviderModalVisible = ref(false)
const editingToolProvider = ref<ToolProviderInfo | null>(null)
interface SchemaField {
  key: string
  title: string
  type: 'string' | 'integer' | 'number' | 'boolean'
  description: string
  default_value: string
  required: boolean
  secret: boolean
}

interface KVRow {
  key: string
  value: string
}

const toolProviderForm = ref<{
  name: string
  provider_key: string
  provider_type: 'http' | 'mcp' | 'custom'
  description: string
  schema_fields: SchemaField[]
  admin_config_rows: KVRow[]
  rate_limit_rows: KVRow[]
  method: 'GET' | 'POST'
  url: string
  headers_rows: KVRow[]
  body_template_text: string
  response_mapping_rows: KVRow[]
}>({ name: '', provider_key: '', provider_type: 'http', description: '', schema_fields: [], admin_config_rows: [], rate_limit_rows: [], method: 'POST', url: '', headers_rows: [], body_template_text: '{}', response_mapping_rows: [] })
const toolProviderFormValid = computed(() => toolProviderForm.value.name.trim() && toolProviderForm.value.provider_key.trim() && toolProviderForm.value.provider_type.trim())

function addSchemaField() {
  toolProviderForm.value.schema_fields.push({ key: '', title: '', type: 'string', description: '', default_value: '', required: true, secret: false })
}
function removeSchemaField(idx: number) {
  toolProviderForm.value.schema_fields.splice(idx, 1)
}
function addKV(rows: KVRow[]) {
  rows.push({ key: '', value: '' })
}
function removeKV(rows: KVRow[], idx: number) {
  rows.splice(idx, 1)
}

function parseKVRows(rows: KVRow[]): Record<string, unknown> | undefined {
  const obj: Record<string, unknown> = {}
  for (const row of rows) {
    if (!row.key.trim()) continue
    const v = row.value.trim()
    if (v === 'true') obj[row.key.trim()] = true
    else if (v === 'false') obj[row.key.trim()] = false
    else if (/^-?\d+$/.test(v)) obj[row.key.trim()] = Number(v)
    else if (/^-?\d+\.\d+$/.test(v)) obj[row.key.trim()] = Number(v)
    else obj[row.key.trim()] = v
  }
  return Object.keys(obj).length ? obj : undefined
}

function providerConfigFromJSON(json: string | object | null) {
  if (!json) return null
  try {
    const obj = typeof json === 'string' ? JSON.parse(json) : json
    const headers: KVRow[] = []
    if (obj.headers) {
      for (const [k, v] of Object.entries(obj.headers)) {
        headers.push({ key: k, value: String(v) })
      }
    }
    const responseMapping: KVRow[] = []
    if (obj.response_mapping) {
      for (const [k, v] of Object.entries(obj.response_mapping)) {
        responseMapping.push({ key: k, value: String(v) })
      }
    }
    return {
      method: (obj.method === 'GET' ? 'GET' : 'POST') as 'GET' | 'POST',
      url: obj.url || '',
      headers_rows: headers,
      body_template_text: obj.body_template ? JSON.stringify(obj.body_template, null, 2) : '{}',
      response_mapping_rows: responseMapping,
    }
  } catch {
    return null
  }
}

function buildProviderConfig(form: typeof toolProviderForm.value): Record<string, unknown> | undefined {
  if (form.provider_type !== 'http') return undefined
  const obj: Record<string, unknown> = {
    method: form.method,
    url: form.url,
  }
  const headers = parseKVRows(form.headers_rows)
  if (headers) obj.headers = headers
  if (form.body_template_text.trim() && form.body_template_text.trim() !== '{}') {
    try {
      obj.body_template = JSON.parse(form.body_template_text)
    } catch {
      throw new Error('请求体模板不是合法 JSON')
    }
  }
  const responseMapping = parseKVRows(form.response_mapping_rows)
  if (responseMapping) obj.response_mapping = responseMapping
  return Object.keys(obj).length > 2 || (obj.url as string).trim() ? obj : undefined
}

function buildConfigSchema(fields: SchemaField[]): Record<string, unknown> | undefined {
  if (!fields.length) return undefined
  const properties: Record<string, unknown> = {}
  const required: string[] = []
  for (const f of fields) {
    if (!f.key.trim()) continue
    const prop: Record<string, unknown> = { type: f.type }
    if (f.title.trim()) prop.title = f.title.trim()
    if (f.description.trim()) prop.description = f.description.trim()
    if (f.type === 'string') {
      if (f.default_value.trim()) prop.default = f.default_value.trim()
      if (f.secret) prop.secret = true
    } else if (f.type === 'integer' || f.type === 'number') {
      if (f.default_value.trim()) {
        const n = Number(f.default_value.trim())
        if (!Number.isNaN(n)) prop.default = n
      }
    } else if (f.type === 'boolean') {
      const v = f.default_value.trim()
      if (v === 'true') prop.default = true
      else if (v === 'false') prop.default = false
    }
    properties[f.key.trim()] = prop
    if (f.required) required.push(f.key.trim())
  }
  if (!Object.keys(properties).length) return undefined
  return { type: 'object', properties, required }
}

function schemaFieldsFromJSON(schema: unknown): SchemaField[] {
  if (!schema || typeof schema !== 'object') return []
  const s = schema as Record<string, unknown>
  if (s.type !== 'object') return []
  const props = s.properties as Record<string, Record<string, unknown>> | undefined
  if (!props) return []
  const required = new Set(Array.isArray(s.required) ? s.required.map(String) : [])
  const fields: SchemaField[] = []
  for (const [key, prop] of Object.entries(props)) {
    const type = String(prop.type || 'string') as SchemaField['type']
    const field: SchemaField = {
      key,
      title: String(prop.title || ''),
      type: ['string', 'integer', 'number', 'boolean'].includes(type) ? type : 'string',
      description: String(prop.description || ''),
      default_value: prop.default !== undefined ? String(prop.default) : '',
      required: required.has(key),
      secret: !!prop.secret,
    }
    fields.push(field)
  }
  return fields
}

function kvRowsFromJSON(obj: unknown): KVRow[] {
  if (!obj || typeof obj !== 'object') return []
  const rows: KVRow[] = []
  for (const [key, value] of Object.entries(obj)) {
    rows.push({ key, value: typeof value === 'string' ? value : JSON.stringify(value) })
  }
  return rows
}

function viewToolProviders(toolType: ToolTypeInfo) {
  currentToolType.value = toolType
  activeTab.value = 'toolProviders'
  loadToolProviders(toolType.id)
}

async function loadToolProviders(toolTypeId: string) {
  try {
    const res = await adminListToolProviders(toolTypeId)
    if (res.code === 0) toolProviders.value = res.data.providers || []
  } catch (e: any) {
    ElMessage.error(e.message || '加载供应商失败')
  }
}

function openToolProviderModal(provider?: ToolProviderInfo) {
  editingToolProvider.value = provider ?? null
  const providerConfig = provider?.provider_config ? providerConfigFromJSON(provider.provider_config) : null
  if (provider) {
    toolProviderForm.value = {
      name: provider.name,
      provider_key: provider.provider_key,
      provider_type: (provider.provider_type as 'http' | 'mcp' | 'custom') || 'http',
      description: provider.description || '',
      schema_fields: schemaFieldsFromJSON(provider.config_schema),
      admin_config_rows: kvRowsFromJSON(provider.admin_config),
      rate_limit_rows: kvRowsFromJSON(provider.rate_limit),
      method: providerConfig?.method || 'POST',
      url: providerConfig?.url || '',
      headers_rows: providerConfig?.headers_rows || [],
      body_template_text: providerConfig?.body_template_text || '{}',
      response_mapping_rows: providerConfig?.response_mapping_rows || [],
    }
  } else {
    toolProviderForm.value = { name: '', provider_key: '', provider_type: 'http', description: '', schema_fields: [], admin_config_rows: [], rate_limit_rows: [], method: 'POST', url: '', headers_rows: [], body_template_text: '{}', response_mapping_rows: [] }
  }
  toolProviderModalVisible.value = true
}

async function saveToolProvider() {
  if (!currentToolType.value) return
  try {
    const providerConfig = buildProviderConfig(toolProviderForm.value)
    if (editingToolProvider.value) {
      const payload: Parameters<typeof adminUpdateToolProvider>[2] = {
        name: toolProviderForm.value.name,
        provider_key: toolProviderForm.value.provider_key,
        provider_type: toolProviderForm.value.provider_type,
        description: toolProviderForm.value.description,
        config_schema: buildConfigSchema(toolProviderForm.value.schema_fields),
        admin_config: parseKVRows(toolProviderForm.value.admin_config_rows),
        rate_limit: parseKVRows(toolProviderForm.value.rate_limit_rows),
      }
      if (providerConfig) payload.provider_config = providerConfig
      await adminUpdateToolProvider(currentToolType.value.id, editingToolProvider.value.id, payload)
    } else {
      const payload: Parameters<typeof adminCreateToolProvider>[1] = {
        name: toolProviderForm.value.name,
        provider_key: toolProviderForm.value.provider_key,
        provider_type: toolProviderForm.value.provider_type,
        description: toolProviderForm.value.description,
        config_schema: buildConfigSchema(toolProviderForm.value.schema_fields),
        admin_config: parseKVRows(toolProviderForm.value.admin_config_rows),
        rate_limit: parseKVRows(toolProviderForm.value.rate_limit_rows),
      }
      if (providerConfig) payload.provider_config = providerConfig
      await adminCreateToolProvider(currentToolType.value.id, payload)
    }
    toolProviderModalVisible.value = false
    await loadToolProviders(currentToolType.value.id)
    await loadToolTypes()
    ElMessage.success(editingToolProvider.value ? '保存成功' : '添加成功')
  } catch (e: any) {
    ElMessage.error(e.message || '保存失败')
  }
}

async function toggleToolProviderEnabled(provider: ToolProviderInfo) {
  if (!currentToolType.value) return
  try {
    await adminUpdateToolProvider(currentToolType.value.id, provider.id, { is_enabled: !provider.is_enabled })
    await loadToolProviders(currentToolType.value.id)
    ElMessage.success(provider.is_enabled ? '停用成功' : '启用成功')
  } catch (e: any) {
    ElMessage.error(e.message || '操作失败')
  }
}

async function deleteToolProvider(id: string) {
  if (!currentToolType.value) return
  try {
    await ElMessageBox.confirm('确定删除这个供应商吗？', '提示', { confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning' })
    await adminDeleteToolProvider(currentToolType.value.id, id)
    await loadToolProviders(currentToolType.value.id)
    await loadToolTypes()
    ElMessage.success('删除成功')
  } catch (e: any) {
    if (e === 'cancel' || e === 'close') return
    ElMessage.error(e.message || '删除失败')
  }
}

watch(activeTab, (tab) => {
  if (tab === 'users') loadUsers()
  if (tab === 'sessions') loadSessions()
  if (tab === 'models') loadModels()
  if (tab === 'tools') loadToolTypes()
})

watch(userSearch, () => loadUsers())
watch([sessionSearch, sessionStatus, sessionPage], () => loadSessions())

onMounted(() => {
  if (activeTab.value === 'users') loadUsers()
  if (activeTab.value === 'sessions') loadSessions()
  if (activeTab.value === 'models') loadModels()
  if (activeTab.value === 'tools') loadToolTypes()
})
</script>
