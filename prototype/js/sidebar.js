// js/sidebar.js
// 通用动态侧边栏渲染

(function() {
    'use strict';

    // 折叠组状态持久化
    const EXPANDED_KEY = 'sidebar_expanded_groups';

    function getExpandedGroups() {
        try {
            return JSON.parse(localStorage.getItem(EXPANDED_KEY) || '[]');
        } catch(e) {
            return [];
        }
    }

    function toggleGroup(groupId) {
        const expanded = getExpandedGroups();
        const idx = expanded.indexOf(groupId);
        if (idx >= 0) {
            expanded.splice(idx, 1);
        } else {
            expanded.push(groupId);
        }
        localStorage.setItem(EXPANDED_KEY, JSON.stringify(expanded));
        renderSidebar(activePageId);
    }

    // 全局activePageId
    window.activePageId = null;

    window.renderSidebar = function(activeId) {
        activePageId = activeId;
        const sidebar = document.getElementById('sidebar');
        if (!sidebar) return;

        const userInfo = JSON.parse(localStorage.getItem('user_info') || '{}');
        const userRole = userInfo.role || 'admin';
        const expandedGroups = getExpandedGroups();

        // 自动展开包含当前页面的组
        MENU_CONFIG.forEach(menu => {
            if (menu.children) {
                const hasActive = menu.children.some(child => child.id === activeId);
                if (hasActive && !expandedGroups.includes(menu.id)) {
                    expandedGroups.push(menu.id);
                    localStorage.setItem(EXPANDED_KEY, JSON.stringify(expandedGroups));
                }
            }
        });

        let html = `
        <div class="p-6">
            <div class="flex items-center gap-3">
                <div class="w-10 h-10 bg-blue-600 rounded-lg flex items-center justify-center">
                    <i class="fas fa-robot text-xl"></i>
                </div>
                <div><h1 class="font-bold text-lg">CodeGuard</h1><p class="text-xs text-gray-400">代码智能门禁</p></div>
            </div>
        </div>
        <nav class="flex-1 px-3 py-4 space-y-1">
        `;

        MENU_CONFIG.forEach(menu => {
            if (!isMenuVisible(menu.role, userRole)) return;

            if (menu.children) {
                // 折叠组
                const isExpanded = expandedGroups.includes(menu.id);
                const hasActiveChild = menu.children.some(child => child.id === activeId);
                const groupActiveClass = hasActiveChild ? 'bg-white/10 border-l-[3px] border-blue-500' : '';

                html += `
                <div class="space-y-1">
                    <div class="flex items-center justify-between px-4 py-3 rounded-lg text-sm cursor-pointer ${groupActiveClass}" onclick="toggleGroup('${menu.id}')">
                        <div class="flex items-center gap-3">
                            <i class="fas ${menu.icon} w-5"></i><span>${menu.name}</span>
                        </div>
                        <i class="fas fa-chevron-down text-xs transition-transform ${isExpanded ? 'rotate-180' : ''}" id="group-icon-${menu.id}"></i>
                    </div>
                    <div class="${isExpanded ? '' : 'hidden'} pl-10 space-y-1">
                `;

                menu.children.forEach(child => {
                    const isActive = child.id === activeId;
                    html += `
                    <a href="${child.href}" class="sidebar-item flex items-center gap-3 px-4 py-2 rounded-lg text-sm ${isActive ? 'active text-white' : 'text-gray-400 hover:text-white'}">
                        <i class="fas ${child.icon} w-4"></i><span>${child.name}</span>
                    </a>
                    `;
                });

                html += `</div></div>`;
            } else {
                // 普通菜单项
                const isActive = menu.id === activeId;
                html += `
                <a href="${menu.href}" class="sidebar-item flex items-center gap-3 px-4 py-3 rounded-lg text-sm ${isActive ? 'active' : ''}">
                    <i class="fas ${menu.icon} w-5"></i><span>${menu.name}</span>
                </a>
                `;
            }
        });

        html += `
        </nav>
        <div class="p-4 border-t border-slate-700">
            <div class="flex items-center gap-3 cursor-pointer" onclick="toggleUserMenu()">
                <div class="w-8 h-8 bg-gray-700 rounded-full flex items-center justify-center">
                    <i class="fas fa-user text-sm"></i>
                </div>
                <div class="text-sm flex-1">
                    <div class="font-medium" id="currentUser">${userInfo.username || '用户'}</div>
                    <div class="text-xs text-gray-400">${userRole === 'admin' ? '管理后台' : '开发者'}</div>
                </div>
                <i class="fas fa-chevron-down text-xs text-gray-400"></i>
            </div>
            <div id="userMenu" class="hidden mt-2 bg-slate-800 rounded-lg py-1">
                ${userRole === 'admin' ? `
                <button onclick="showChangePasswordModal()" class="w-full text-left px-4 py-2 text-sm text-gray-300 hover:bg-slate-700 hover:text-white transition-colors">
                    <i class="fas fa-key w-5"></i>修改密码
                </button>
                ` : ''}
                <button onclick="logout()" class="w-full text-left px-4 py-2 text-sm text-gray-300 hover:bg-slate-700 hover:text-white transition-colors">
                    <i class="fas fa-sign-out-alt w-5"></i>退出登录
                </button>
            </div>
        </div>
        `;

        sidebar.innerHTML = html;
    };

    // 暴露toggleGroup到全局
    window.toggleGroup = toggleGroup;

    // 默认在DOMReady时渲染（页面可覆盖activeId）
    document.addEventListener('DOMContentLoaded', function() {
        if (window.activePageId) {
            renderSidebar(window.activePageId);
        }
    });
})();
