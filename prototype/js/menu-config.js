// js/menu-config.js
// 全局菜单配置 - 按角色控制可见性
const MENU_CONFIG = [
    // ========== 公共菜单 ==========
    {
        id: 'dashboard',
        name: '首页',
        href: 'statistics.html',
        icon: 'fa-chart-line',
        role: ['admin', 'user']
    },
    {
        id: 'mr-stats',
        name: '代码提交统计',
        href: 'mr-stats.html',
        icon: 'fa-code-branch',
        role: ['admin', 'user']
    },
    {
        id: 'tasks',
        name: '任务列表',
        href: 'tasks.html',
        icon: 'fa-tasks',
        role: ['admin', 'user']
    },

    // ========== Admin专属 ==========
    {
        id: 'projects',
        name: '项目管理',
        href: 'projects.html',
        icon: 'fa-folder-open',
        role: ['admin']
    },
    {
        id: 'pools',
        name: '任务资源池',
        href: 'pools.html',
        icon: 'fa-server',
        role: ['admin']
    },
    {
        id: 'models',
        name: '大模型管理',
        href: 'models.html',
        icon: 'fa-brain',
        role: ['admin']
    },

    // ========== 通知管理（折叠组）==========
    {
        id: 'notify-group',
        name: '通知管理',
        icon: 'fa-bell',
        role: ['admin'],
        children: [
            { id: 'notifiers', name: '企业微信', href: 'notifiers.html', icon: 'fa-comment' },
            { id: 'mail',      name: '邮件',     href: 'mail.html',     icon: 'fa-envelope' },
            { id: 'mappings',  name: '成员映射', href: 'member-mappings.html', icon: 'fa-users' },
            { id: 'report',    name: '报告管理', href: 'report.html',   icon: 'fa-file-alt' },
        ]
    },

    // ========== 系统管理（折叠组）==========
    {
        id: 'settings-group',
        name: '系统管理',
        icon: 'fa-cog',
        role: ['admin'],
        children: [
            { id: 'settings-config',   name: '系统配置',     href: 'settings.html?tab=config',       icon: 'fa-sliders-h' },
            { id: 'settings-ai',       name: 'AI对话模板',   href: 'settings.html?tab=aitemplate',   icon: 'fa-robot' },
            { id: 'settings-review',   name: '代码审查模板', href: 'settings.html?tab=reviewtemplate', icon: 'fa-code' },
            { id: 'settings-logs',     name: '操作日志',     href: 'settings.html?tab=logs',         icon: 'fa-history' },
            { id: 'settings-info',     name: '系统信息',     href: 'settings.html?tab=info',         icon: 'fa-info-circle' },
        ]
    },
];

// 检查菜单是否对当前角色可见
function isMenuVisible(menuRole, userRole) {
    if (!menuRole) return true;
    return menuRole.includes(userRole);
}
